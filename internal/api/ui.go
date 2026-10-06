package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/digest"
	"registry/internal/registry"
	"registry/internal/storage"
)

// RegisterUI mounts a small JSON API consumed by the web UI.
func (h *Handler) RegisterUI(r gin.IRouter) {
	v1 := r.Group("/api/v1")
	v1.GET("/repositories", h.uiRepositories)
	// Repository names contain slashes ("apeiron/iot-hub"). A :name parameter
	// only matches one segment, and percent-encoding does not help because the
	// server decodes %2F back to "/" before routing — so every namespaced
	// repository fell through to the SPA catch-all and the UI received HTML
	// where it expected JSON. Match the whole remainder and split it here.
	v1.GET("/repositories/*rest", h.uiRepoSub)
	v1.GET("/stats", h.uiStats)
	v1.GET("/me", h.uiMe)
	v1.GET("/registries", h.uiRegistries)
	v1.GET("/browse", h.uiBrowse)
	v1.GET("/search", h.uiSearch)
	v1.GET("/artifact", h.uiArtifact)
	v1.POST("/account/password", h.uiChangePassword)
	v1.GET("/account/keys", h.accountListKeys)
	v1.POST("/account/keys", h.accountCreateKey)
	v1.DELETE("/account/keys/:key", h.accountRevokeKey)
	v1.GET("/roles", h.uiRolesList)

	admin := v1.Group("/admin")
	admin.Use(h.requireAdmin)
	{
		admin.GET("/users", h.adminListUsers)
		admin.POST("/users", h.adminCreateUser)
		admin.PUT("/users/:name", h.adminUpdateUser)
		admin.DELETE("/users/:name", h.adminDeleteUser)
		admin.GET("/users/:name/roles", h.adminGetUserRoles)
		admin.PUT("/users/:name/roles", h.adminSetUserRoles)

		admin.GET("/service-accounts", h.adminListKeys)
		admin.POST("/service-accounts", h.adminCreateKey)
		admin.DELETE("/service-accounts/:key", h.adminRevokeKey)

		admin.GET("/repositories", h.adminListRepos)
		admin.DELETE("/repositories/:name", h.adminDeleteRepo)

		// Authorization. These replace the previous roles endpoints, which wrote
		// to a column no authorization path ever read.
		admin.GET("/permissions", h.adminPermissions)

		admin.GET("/roles", h.authzListRoles)
		admin.POST("/roles", h.authzUpsertRole)
		admin.PUT("/roles/:name", h.authzUpsertRole)
		admin.DELETE("/roles/:name", h.authzDeleteRole)

		admin.GET("/groups", h.authzListGroups)
		admin.POST("/groups", h.authzUpsertGroup)
		admin.PUT("/groups/:name", h.authzUpsertGroup)
		admin.DELETE("/groups/:name", h.authzDeleteGroup)

		admin.GET("/grants", h.authzListBindings)
		admin.POST("/grants", h.authzAddBinding)
		admin.PUT("/grants/:id", h.authzUpdateBinding)
		admin.DELETE("/grants/:id", h.authzDeleteBinding)

		// Anonymous identities: named callers with no credentials, each with
		// its own recognition rules and its own grants.
		admin.GET("/anonymous", h.authzListAnonymous)
		admin.POST("/anonymous", h.authzUpsertAnonymous)
		admin.PUT("/anonymous/:name", h.authzUpsertAnonymous)
		admin.DELETE("/anonymous/:name", h.authzDeleteAnonymous)

		// Credentials. Values are write-only: they can be created, rotated and
		// deleted, never read back.
		admin.GET("/secrets", h.adminListSecrets)
		admin.POST("/secrets", h.adminCreateSecret)
		admin.PUT("/secrets/:key", h.adminUpdateSecret)
		admin.DELETE("/secrets/:key", h.adminDeleteSecret)

		// Blob stores as named entities, referenced by registries.
		admin.GET("/blob-stores", h.adminListBlobStores)
		admin.POST("/blob-stores", h.adminUpsertBlobStore)
		admin.GET("/blob-stores/:name", h.adminGetBlobStore)
		admin.PUT("/blob-stores/:name", h.adminUpsertBlobStore)
		admin.DELETE("/blob-stores/:name", h.adminDeleteBlobStore)

		// Global settings (e.g. anonymous access toggle).
		admin.GET("/settings", h.adminGetSettings)
		admin.PUT("/settings", h.adminSetSettings)

		// Single sign-on providers (browser login, UI-managed; secrets are
		// write-only and never listed back).
		admin.GET("/sso/presets", h.adminListSSOPresets)
		admin.GET("/sso/providers", h.adminListSSOProviders)
		admin.POST("/sso/providers", h.adminCreateSSOProvider)
		admin.PUT("/sso/providers/:id", h.adminUpdateSSOProvider)
		admin.DELETE("/sso/providers/:id", h.adminDeleteSSOProvider)

		// Registry definitions (hosted / proxy / group).
		admin.GET("/registries", h.adminListRegistries)
		admin.GET("/registries/:name", h.adminGetRegistry)
		admin.POST("/registries", h.adminCreateRegistry)
		admin.PUT("/registries/:name", h.adminUpdateRegistry)
		admin.DELETE("/registries/:name", h.adminDeleteRegistry)
		admin.POST("/registries/:name/warm", h.adminWarmRegistry)
	}
}

// registryParam returns the registry name from the query string, defaulting to
// the first configured registry.
func (h *Handler) registryParam(c *gin.Context) string {
	if name := c.Query("registry"); name != "" {
		return name
	}
	list := h.mgr.List()
	if len(list) > 0 {
		return list[0].Name
	}
	return ""
}

// storeFor returns the metadata store for the registry named by the request,
// but only when the current principal is allowed to read that registry. The
// registry name arrives from a caller-supplied query parameter, so it must be
// authorised here: this is the choke point every UI read handler goes through.
//
// A denied registry yields nil, which callers report as 404 rather than 403 —
// deliberately, so the response does not reveal that a registry the caller may
// not read exists.
func (h *Handler) storeFor(c *gin.Context) *storage.Store {
	name := h.registryParam(c)
	reg, ok := h.mgr.Get(name)
	if !ok {
		return nil
	}
	if !h.allowAccess(c, reg, false) {
		return nil
	}
	return h.mgr.StoreFor(name)
}

func (h *Handler) uiRepositories(c *gin.Context) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repos, err := st.ListRepos()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if repos == nil {
		repos = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"registry": h.registryParam(c), "repositories": repos})
}

// uiRepoSub dispatches /repositories/<name>/tags and
// /repositories/<name>/manifests/<ref>, where <name> may contain slashes.
func (h *Handler) uiRepoSub(c *gin.Context) {
	kind, repo, ref := parseRepoSub(c.Param("rest"))
	switch kind {
	case "list":
		h.uiRepositories(c)
	case "tags":
		h.uiTagsFor(c, repo)
	case "manifest":
		h.uiManifestFor(c, repo, ref)
	default:
		c.JSON(http.StatusNotFound, gin.H{"error": "unknown endpoint"})
	}
}

// parseRepoSub splits the catch-all remainder of /api/v1/repositories/… into
// the operation and its repository, tolerating slashes inside the repository
// name. Returns kind "" when nothing matches.
func parseRepoSub(rest string) (kind, repo, ref string) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "list", "", ""
	}
	if r, ok := strings.CutSuffix(rest, "/tags"); ok && r != "" {
		return "tags", r, ""
	}
	if i := strings.LastIndex(rest, "/manifests/"); i > 0 {
		r, f := rest[:i], rest[i+len("/manifests/"):]
		if r != "" && f != "" {
			return "manifest", r, f
		}
	}
	return "", "", ""
}

func (h *Handler) uiTags(c *gin.Context) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repo := c.Param("name")
	h.tagsBody(c, st, repo)
}

// uiTagsFor is uiTags with the repository supplied by the dispatcher.
func (h *Handler) uiTagsFor(c *gin.Context, repo string) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	h.tagsBody(c, st, repo)
}

func (h *Handler) tagsBody(c *gin.Context, st *storage.Store, repo string) {
	tags, err := st.ListTags(repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if tags == nil {
		tags = []string{}
	}
	c.JSON(http.StatusOK, gin.H{"name": repo, "tags": tags})
}

func (h *Handler) uiManifest(c *gin.Context) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repo := c.Param("name")
	ref := c.Param("ref")
	h.manifestBody(c, st, repo, ref)
}

// uiManifestFor is uiManifest with repository and reference from the dispatcher.
func (h *Handler) uiManifestFor(c *gin.Context, repo, ref string) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	h.manifestBody(c, st, repo, ref)
}

func (h *Handler) manifestBody(c *gin.Context, st *storage.Store, repo, ref string) {
	content, mt, err := st.GetManifest(repo, ref)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "manifest not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	d := digest.FromBytes(content)
	c.JSON(http.StatusOK, gin.H{
		"name":       repo,
		"reference":  ref,
		"digest":     d.String(),
		"media_type": mt,
		"manifest":   jsonRaw(content),
	})
}

func (h *Handler) uiStats(c *gin.Context) {
	st := h.storeFor(c)
	if st == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	repos, _ := st.ListRepos()
	totalTags := 0
	for _, r := range repos {
		t, _ := st.ListTags(r)
		totalTags += len(t)
	}
	c.JSON(http.StatusOK, gin.H{
		"registry":     h.registryParam(c),
		"repositories": len(repos),
		"tags":         totalTags,
	})
}

// uiRegistries lists registry definitions (name/type/format/online plus the
// routing coordinates hosts, port and base path) for the UI selector. This is
// safe to expose: it contains no secrets.
func (h *Handler) uiRegistries(c *gin.Context) {
	out := make([]gin.H, 0)
	for _, r := range h.mgr.List() {
		hosts := r.Hosts
		if hosts == nil {
			hosts = []string{}
		}
		out = append(out, gin.H{
			"name":      r.Name,
			"type":      r.Type,
			"format":    r.Format,
			"online":    r.Online,
			"hosts":     hosts,
			"port":      r.Port,
			"base_path": r.BasePath,
		})
	}
	c.JSON(http.StatusOK, gin.H{"registries": out})
}

// uiBrowse lists the artifacts stored in a single registry. For docker/OCI
// registries it returns the repository list; for path-based formats it returns
// the stored object paths. Read access is enforced through allowAccess.
func (h *Handler) uiBrowse(c *gin.Context) {
	name := c.Query("registry")
	if name == "" {
		name = h.registryParam(c)
	}
	reg, ok := h.mgr.Get(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	if !h.allowAccess(c, reg, false) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}
	limit := atoiDefault(c.Query("limit"), 500)
	if limit > 2000 {
		limit = 2000
	}
	prefix := c.Query("prefix")
	if reg.Format == string(registry.FormatDocker) {
		st := h.mgr.StoreFor(name)
		if st == nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
			return
		}
		repos, err := st.ListRepos()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		q := strings.ToLower(prefix)
		entries := make([]gin.H, 0)
		for _, r := range repos {
			if q != "" && !strings.Contains(strings.ToLower(r), q) {
				continue
			}
			entries = append(entries, gin.H{"path": r, "name": r, "dir": true})
			if len(entries) >= limit {
				break
			}
		}
		c.JSON(http.StatusOK, gin.H{
			"registry": name, "format": reg.Format, "type": reg.Type, "entries": entries,
		})
		return
	}
	be, err := h.mgr.ArtifactBackendFor(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer be.Close()
	objs, err := be.List(prefix)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	entries := make([]gin.H, 0, len(objs))
	for _, o := range objs {
		if len(entries) >= limit {
			break
		}
		entries = append(entries, gin.H{"path": o, "name": path.Base(o), "dir": false})
	}
	c.JSON(http.StatusOK, gin.H{
		"registry": name, "format": reg.Format, "type": reg.Type, "entries": entries,
	})
}

// uiSearch performs a global, cross-registry substring search over artifact
// names. Each registry is searched only if the caller has read access, and the
// optional format filter narrows the result set. Deterministic: results are
// bounded by the limit and returned flat.
func (h *Handler) uiSearch(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "q required"})
		return
	}
	format := c.Query("format")
	limit := atoiDefault(c.Query("limit"), 200)
	if limit > 1000 {
		limit = 1000
	}
	ql := strings.ToLower(q)
	results := make([]gin.H, 0)
	for _, reg := range h.mgr.List() {
		if !h.allowAccess(c, reg, false) {
			continue
		}
		if format != "" && reg.Format != format {
			continue
		}
		if reg.Format == string(registry.FormatDocker) {
			st := h.mgr.StoreFor(reg.Name)
			if st == nil {
				continue
			}
			repos, err := st.ListRepos()
			if err != nil {
				continue
			}
			for _, r := range repos {
				if len(results) >= limit {
					break
				}
				if !strings.Contains(strings.ToLower(r), ql) {
					continue
				}
				results = append(results, gin.H{
					"registry": reg.Name, "format": reg.Format, "path": r,
					"name": r, "kind": "repo",
				})
			}
		} else {
			be, err := h.mgr.ArtifactBackendFor(reg.Name)
			if err != nil {
				continue
			}
			objs, err := be.List("")
			be.Close()
			if err != nil {
				continue
			}
			for _, o := range objs {
				if len(results) >= limit {
					break
				}
				if !strings.Contains(strings.ToLower(o), ql) {
					continue
				}
				results = append(results, gin.H{
					"registry": reg.Name, "format": reg.Format, "path": o,
					"name": path.Base(o), "kind": "artifact",
				})
			}
		}
		if len(results) >= limit {
			break
		}
	}
	c.JSON(http.StatusOK, gin.H{"query": q, "results": results})
}

// uiArtifact streams a single artifact object to the caller. This surface is
// registry-scoped (no host resolution) and read-gated. Docker/OCI content is
// served through the OCI API instead, so it is rejected here with a clear error.
func (h *Handler) uiArtifact(c *gin.Context) {
	name := c.Query("registry")
	p := c.Query("path")
	if name == "" || p == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "registry and path required"})
		return
	}
	reg, ok := h.mgr.Get(name)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return
	}
	if reg.Format == string(registry.FormatDocker) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "docker artifacts use the OCI API"})
		return
	}
	if !h.allowAccess(c, reg, false) {
		c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "access denied"})
		return
	}
	be, err := h.mgr.ArtifactBackendFor(name)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer be.Close()
	rc, sz, ct, err := be.Get(p)
	if err != nil {
		if err == registry.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rc.Close()
	c.Header("Content-Type", ct)
	c.Header("Content-Disposition", "attachment; filename=\""+path.Base(p)+"\"")
	if sz >= 0 {
		c.Header("Content-Length", strconv.FormatInt(sz, 10))
	}
	c.Status(http.StatusOK)
	io.Copy(c.Writer, rc)
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func (h *Handler) uiMe(c *gin.Context) {
	u := h.currentUser(c)
	if u == nil {
		c.JSON(http.StatusOK, gin.H{"user": gin.H{"name": "anonymous", "anonymous": true}})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"name":                     u.Name,
			"admin":                    h.isAdmin(c),
			"groups":                   u.Groups,
			"anonymous":                u.Anonymous,
			"password_change_required": u.PasswordChangeRequired,
			"key_label":                u.KeyLabel,
		},
	})
}

// uiChangePassword lets an authenticated local user change their own password.
// It verifies the current password and clears the password_change_required
// flag, which is how the seeded first-login admin is forced to set a real
// password.
func (h *Handler) uiChangePassword(c *gin.Context) {
	u := h.currentUser(c)
	if u == nil || u.IsAnonymous() {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
		return
	}
	local := h.authMgr.Local()
	if local == nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "local authentication is not enabled"})
		return
	}
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	if body.NewPassword == "" {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "new_password is required"})
		return
	}
	if err := local.ChangePassword(c.Request.Context(), u.Name, body.CurrentPassword, body.NewPassword); err != nil {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// currentUser extracts the authenticated user set by the auth middleware.
func (h *Handler) currentUser(c *gin.Context) *auth.User {
	if u, ok := c.Get("user"); ok {
		if user, ok := u.(*auth.User); ok {
			return user
		}
	}
	return nil
}

// jsonRaw parses content into an interface for pretty JSON embedding.
func jsonRaw(b []byte) interface{} {
	var v interface{}
	if err := json.Unmarshal(b, &v); err == nil {
		return v
	}
	return string(b)
}

// requireAdmin now lives in rbac.go, where it is answered by the grants
// rather than by a boolean on the user row.
