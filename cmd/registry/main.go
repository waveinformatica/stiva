package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jackc/pgx/v5/pgxpool"

	"registry/internal/api"
	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/blobstore"
	"registry/internal/config"
	"registry/internal/jobs"
	"registry/internal/registry"
	"registry/internal/storage"
	"registry/internal/vault"
	"registry/internal/webhooks"
)

// pinnedKey carries the registry name bound to a dedicated TCP listener.

func main() {
	cfg := config.Load()
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	// Metadata store: PostgreSQL (required, no local store).
	meta, err := storage.OpenMetadataStore(cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("cannot open metadata store: %v", err)
	}
	defer meta.Close()

	// Vault: credentials referenced by stores and providers are encrypted at
	// rest. Without a master key the vault still constructs, but refuses every
	// operation rather than silently keeping values in cleartext.
	pool, err := pgxpool.New(context.Background(), cfg.PostgresDSN)
	if err != nil {
		log.Fatalf("cannot open credential pool: %v", err)
	}
	defer pool.Close()

	vlt, err := vault.New(pool, os.Getenv("REGISTRY_VAULT_KEY"))
	if err != nil {
		log.Fatalf("vault: %v", err)
	}
	if !vlt.Enabled() {
		log.Printf("WARNING: REGISTRY_VAULT_KEY is not set; stored credentials " +
			"cannot be encrypted and any store that needs one will fail to build. " +
			"Generate one with: openssl rand -hex 32")
	}

	stores := blobstore.NewRepo(pool, vlt)

	// Registry manager: owns every registry (hosted/proxy/group) and routes by
	// host or dedicated TCP port. Definitions live in PostgreSQL.
	mgr := registry.NewManager(meta, cfg.UploadsDir)
	mgr.SetBlobResolver(func(name string) (storage.BlobConfig, bool, error) {
		ctx := context.Background()
		storeName, prefix, err := stores.RegistryLink(ctx, name)
		if err != nil || storeName == "" {
			return storage.BlobConfig{}, false, nil
		}
		st, err := stores.Get(ctx, storeName)
		if err != nil {
			return storage.BlobConfig{}, false, fmt.Errorf("store %q: %w", storeName, err)
		}
		blob, err := stores.Resolve(ctx, st, prefix)
		if err != nil {
			return storage.BlobConfig{}, false, err
		}
		return blob, true, nil
	})
	if err := mgr.Load(); err != nil {
		log.Fatalf("cannot load registries: %v", err)
	}

	// Authentication.
	authMgr := auth.NewManager(cfg.Auth, cfg.PostgresDSN)
	if err := authMgr.BootstrapAdmin(cfg.AdminUser, cfg.AdminPass); err != nil {
		log.Fatalf("cannot bootstrap admin: %v", err)
	}
	// UI-managed SSO providers (database records + vault secrets, reloaded
	// without restarts whenever the admin UI changes them).
	authMgr.SetSSOBackend(meta, vlt)
	if err := authMgr.ReloadSSO(context.Background()); err != nil {
		log.Printf("auth: sso reload: %v", err)
	}

	// Authorization: one model, loaded once and refreshed when the rules change.
	az := authz.NewEngine(pool)
	if err := az.Reload(context.Background()); err != nil {
		log.Fatalf("cannot load authorization rules: %v", err)
	}
	// Way back in when the last grant is lost: naming an administrator here
	// re-asserts it at startup.
	if err := az.EnsureAdmin(context.Background(), cfg.AdminUser); err != nil {
		log.Fatalf("cannot ensure administrator: %v", err)
	}
	// Declared service accounts (keys from mounted Secrets) are made to match
	// the declaration at every start, an empty one included.
	if lr := authMgr.Local(); lr != nil {
		if err := lr.ProvisionServiceAccounts(context.Background(), cfg.ServiceAccounts, os.ReadFile); err != nil {
			log.Fatalf("cannot provision service accounts: %v", err)
		}
		if err := az.Reload(context.Background()); err != nil {
			log.Fatalf("cannot load authorization rules: %v", err)
		}
		log.Printf("service accounts: %d declared", len(cfg.ServiceAccounts))
	} else if len(cfg.ServiceAccounts) > 0 {
		log.Fatalf("service accounts are declared but the local realm is not enabled")
	}

	handler := api.NewHandler(mgr, authMgr)

	if cs, ok := meta.(storage.CatalogStore); ok {
		handler.SetCatalog(cs)
	} else {
		log.Printf("WARNING: metadata store has no catalog support; the catalog is disabled")
	}

	// One context for the background workers (jobs, webhook delivery).
	jobCtx, stopJobs := context.WithCancel(context.Background())
	defer stopJobs()

	// Webhooks: event notifications delivered from a PostgreSQL-backed queue.
	var dispatcher *webhooks.Dispatcher
	if ws, ok := meta.(storage.WebhookStore); ok {
		dispatcher = webhooks.NewDispatcher(ws, func(ctx context.Context, ref string) (string, error) {
			return vlt.Resolve(ctx, vault.ScopeCredentials, vault.SecretRef(ref))
		})
		dispatcher.Start(jobCtx)
		handler.SetWebhooks(dispatcher, ws)
	} else {
		log.Printf("WARNING: metadata store has no webhook support; webhooks are disabled")
	}

	// Background jobs: retention cleanup and garbage collection, on demand or
	// on a schedule, from a PostgreSQL-backed queue.
	if js, ok := meta.(storage.JobStore); ok {
		runner := jobs.NewRunner(js)
		runner.Handle(jobs.KindCleanup, func(ctx context.Context, j storage.Job) (any, error) {
			return mgr.Cleanup(ctx, j.Registry, j.DryRun)
		}, func(name string) bool {
			reg, ok := mgr.Get(name)
			return ok && registry.CleanupSupported(reg) == nil && reg.Cleanup.Active()
		})
		runner.Handle(jobs.KindGC, func(ctx context.Context, j storage.Job) (any, error) {
			var p struct {
				OlderThan string `json:"older_than"`
			}
			_ = json.Unmarshal(j.Params, &p)
			older := time.Hour
			if p.OlderThan != "" {
				d, err := time.ParseDuration(p.OlderThan)
				if err != nil || d < 0 {
					return nil, fmt.Errorf("older_than must be a Go duration like 1h, got %q", p.OlderThan)
				}
				older = d
			}
			return mgr.GC(ctx, j.Registry, older, j.DryRun)
		}, func(name string) bool { _, ok := mgr.Get(name); return ok })
		runner.Handle(jobs.KindReconcile, func(ctx context.Context, j storage.Job) (any, error) {
			return mgr.Reconcile(ctx, j.Registry, j.DryRun)
		}, func(name string) bool { return mgr.ReconcilableBlobs(name) })
		runner.OnFinish = func(j storage.Job, status string, err error) {
			if dispatcher == nil {
				return
			}
			data := map[string]any{"id": j.ID, "kind": j.Kind, "status": status, "dry_run": j.DryRun, "trigger": j.Trigger}
			if err != nil {
				data["error"] = err.Error()
			}
			dispatcher.Emit(webhooks.Event{Name: webhooks.EventJobFinished, Registry: j.Registry, Data: data})
		}
		runner.Start(jobCtx)
		handler.SetJobs(runner, js)
	} else {
		log.Printf("WARNING: metadata store has no job support; background jobs are disabled")
	}
	handler.SetStorage(vlt, stores)
	handler.SetAuthz(az)
	if ss, ok := meta.(storage.ScanStore); ok {
		handler.SetScans(ss)
		handler.SetFindings(meta.(storage.FindingsStore))
	} else {
		log.Printf("WARNING: metadata store has no scan support; image scanning is disabled")
	}

	if os.Getenv("REGISTRY_DEBUG") == "" {
		gin.SetMode(gin.ReleaseMode)
	}
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())

	// Health endpoint: deliberately unauthenticated and registered outside the
	// protected group. Kubernetes probes must not depend on the anonymous-access
	// setting — pointing them at /v2/ makes the pod unschedulable whenever
	// anonymous access is off, which is the default.
	r.GET("/healthz", func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	// Token endpoint must be reachable without authentication.
	r.GET("/auth/token", authMgr.TokenHandler)
	r.POST("/auth/token", authMgr.TokenHandler)
	// Browser single-sign-on: provider list, login start and callback. Public
	// by design — the login page needs the buttons before any session exists.
	r.GET("/auth/sso", authMgr.SSOListHandler)
	r.GET("/auth/sso/:id/login", authMgr.SSOLoginHandler)
	r.GET("/auth/sso/:id/callback", authMgr.SSOCallbackHandler)
	r.POST("/auth/sso/:id/callback", authMgr.SSOCallbackHandler)
	r.GET("/auth/me", authMgr.Middleware(), func(c *gin.Context) {
		if u, ok := c.Get("user"); ok {
			c.JSON(http.StatusOK, u)
		} else {
			c.JSON(http.StatusOK, gin.H{"user": "anonymous"})
		}
	})

	// Protect the OCI API and the UI JSON API.
	protected := r.Group("/")
	protected.Use(authMgr.Middleware())
	{
		handler.Register(protected)
		handler.RegisterUI(protected)
		handler.RegisterMetrics(protected)
	}

	// Serve the web UI if a build directory is provided/mounted. Known
	// application paths (every UI page, plus the static assets index.html
	// refers to) are registered explicitly so the server knows them: each
	// serves index.html unauthenticated, ahead of the artifact dispatcher
	// (which would otherwise demand authentication on artifact hosts and
	// make the login page unreachable there) and ahead of the generic
	// fallback below, which keeps serving unknown paths as the SPA.
	webDir := os.Getenv("REGISTRY_WEB_DIR")
	if webDir == "" {
		webDir = "web/dist"
	}
	for _, p := range uiPagePaths {
		r.GET(p, func(c *gin.Context) { serveWebUIFile(c, webDir) })
	}
	// An application page: /catalog/<slug>.
	r.GET("/catalog/:slug", func(c *gin.Context) { serveWebUIFile(c, webDir) })
	for _, p := range uiStaticPaths {
		r.GET(p, func(c *gin.Context) { serveWebUIFile(c, webDir) })
	}
	r.GET("/assets/*filepath", func(c *gin.Context) { serveWebUIFile(c, webDir) })
	r.NoRoute(func(c *gin.Context) {
		// Artifact registries (helm/maven/npm/…) are resolved by Host and stay
		// behind authentication. Everything else falls through to the static
		// SPA, which is served unauthenticated on purpose: it carries the login
		// form, and the JSON API it calls (/api/v1, /v2) is protected on its
		// own. Requiring auth here would make the UI impossible to reach.
		if handler.IsArtifactHost(c) {
			authMgr.Middleware()(c)
			if c.IsAborted() {
				return
			}
			if handler.ArtifactDispatch(c) {
				return
			}
		}
		// An unknown API path is an error, not the application page: a client
		// calling an endpoint this version does not have must not read a 200.
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.JSON(http.StatusNotFound, gin.H{"error": "no such API endpoint"})
			return
		}
		serveWebUIFile(c, webDir)
	})

	// Start a dedicated TCP listener for every registry that requests one.
	mainPort := portOf(cfg.ListenAddr)
	for _, reg := range mgr.List() {
		if reg.Port > 0 && reg.Port != mainPort {
			startPinnedListener(reg.Port, reg.Name, r)
		}
	}

	log.Printf("registry listening on %s (auth=on, registries=%d)",
		cfg.ListenAddr, len(mgr.List()))
	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           r,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// Timeouts applied to every listener. Only the header read and the idle
// connection are bounded: a registry streams multi-gigabyte blobs in both
// directions, so an overall ReadTimeout or WriteTimeout would truncate
// legitimate pushes and pulls. ReadHeaderTimeout is what closes the Slowloris
// hole — a client cannot hold a connection open by dribbling out headers.
const (
	readHeaderTimeout = 20 * time.Second
	idleTimeout       = 120 * time.Second
)

// startPinnedListener runs an HTTP server whose requests are pinned to a single
// registry, so clients can address it by port alone (no Host header needed).
func startPinnedListener(port int, name string, next http.Handler) {
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req = req.WithContext(context.WithValue(req.Context(), registry.PinnedContextKey, name))
		next.ServeHTTP(w, req)
	})
	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		IdleTimeout:       idleTimeout,
	}
	go func() {
		log.Printf("registry %q pinned to TCP :%d", name, port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("listener :%d error: %v", port, err)
		}
	}()
}

func portOf(addr string) int {
	if i := lastColon(addr); i >= 0 {
		p, err := strconv.Atoi(addr[i+1:])
		if err == nil {
			return p
		}
	}
	return 0
}

func lastColon(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == ':' {
			return i
		}
	}
	return -1
}

// uiPagePaths are the frontend routes every UI page lives at (see
// VIEW_PATHS in web/src/lib/routes.ts — keep the two lists in sync).
var uiPagePaths = []string{
	"/",
	"/explorer",
	"/apikeys",
	"/admin/users",
	"/admin/service-accounts",
	"/admin/registries",
	"/admin/stores",
	"/admin/credentials",
	"/admin/roles",
	"/admin/groups",
	"/admin/grants",
	"/admin/anonymous",
	"/admin/sso",
	"/admin/settings",
	"/admin/jobs",
	"/admin/catalog",
	"/catalog",
	"/admin/webhooks",
};

// uiStaticPaths are the root-level build assets index.html refers to.
var uiStaticPaths = []string{
	"/stiva-mark.png",
	"/stiva-logo.png",
};

// serveWebUIFile serves a static SPA build from dir (if it exists) under /.
func serveWebUIFile(c *gin.Context, dir string) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		c.Status(http.StatusNotFound)
		return
	}
	p := filepath.Join(dir, c.Request.URL.Path)
	if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
		c.File(p)
		return
	}
	c.File(filepath.Join(dir, "index.html"))
}
