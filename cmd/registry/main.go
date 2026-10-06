package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/jackc/pgx/v5/pgxpool"

	"registry/internal/api"
	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/blobstore"
	"registry/internal/config"
	"registry/internal/registry"
	"registry/internal/storage"
	"registry/internal/vault"
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
		blob, err := st.Resolve(ctx, vlt, prefix)
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

	handler := api.NewHandler(mgr, authMgr)
	handler.SetStorage(vlt, stores)
	handler.SetAuthz(az)

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
	}

	// Serve the web UI if a build directory is provided/mounted. The catch-all
	// first tries the artifact dispatcher (helm/maven/npm), then falls back to
	// the static SPA for unmatched paths.
	webDir := os.Getenv("REGISTRY_WEB_DIR")
	if webDir == "" {
		webDir = "web/dist"
	}
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
