package api

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/authz"
	"registry/internal/blobstore"
	"registry/internal/digest"
	"registry/internal/registry"
	"registry/internal/storage"
	"registry/internal/vault"
)

// repoNameRegex validates OCI repository names (case-insensitive, slash-separated).
var repoNameRegex = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)

// isWriteMethod reports whether an HTTP method mutates registry state.
func isWriteMethod(m string) bool {
	switch m {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// Handler implements the OCI Distribution API on top of the registry manager.
// Every request is routed to the correct registry (hosted/proxy/group) by the
// manager before any operation runs.
type Handler struct {
	mgr     *registry.Manager
	authMgr *auth.Manager

	// Credential vault and blob store repository, wired by SetStorage.
	vault  *vault.Vault
	stores *blobstore.Repo
	authz  *authz.Engine
}

// NewHandler builds an API handler.
func NewHandler(mgr *registry.Manager, authMgr *auth.Manager) *Handler {
	return &Handler{mgr: mgr, authMgr: authMgr}
}

// Register mounts the OCI endpoints onto a gin router at /v2.
func (h *Handler) Register(r gin.IRouter) {
	r.GET("/v2", h.Base)
	r.Any("/v2/*path", h.Dispatch)
}

func (h *Handler) setVersion(c *gin.Context) {
	c.Header("Docker-Distribution-API-Version", "registry/2.0")
}

// Base handles GET /v2/ (API version check).
func (h *Handler) Base(c *gin.Context) {
	h.setVersion(c)
	c.Status(http.StatusOK)
}

// resolve maps the request to its registry and backend.
func (h *Handler) resolve(c *gin.Context) (*registry.Registry, registry.Backend, error) {
	pinnedName := ""
	if v := c.Request.Context().Value(registry.PinnedContextKey); v != nil {
		if s, ok := v.(string); ok {
			pinnedName = s
		}
	}
	reg, _, err := h.mgr.Resolve(c.Request.Host, pinnedName, strings.Trim(c.Request.URL.Path, "/"))
	if err != nil {
		return nil, nil, err
	}
	be, err := h.mgr.BackendFor(reg.Name)
	if err != nil {
		return nil, nil, err
	}
	return reg, be, nil
}

// Dispatch parses the OCI path and routes to the appropriate operation.
func (h *Handler) Dispatch(c *gin.Context) {
	h.setVersion(c)

	// Anonymous principals may pull but not push.
	if isWriteMethod(c.Request.Method) {
		if u, ok := c.Get("user"); ok {
			if usr, ok2 := u.(*auth.User); ok2 && usr.IsAnonymous() {
				c.Header("WWW-Authenticate", h.authMgr.Challenge(c))
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"errors": []gin.H{{"code": "DENIED", "message": "authentication required to push"}},
				})
				return
			}
		}
	}

	path := strings.Trim(c.Param("path"), "/")
	if path == "" {
		h.Base(c)
		return
	}

	reg, be, err := h.resolve(c)
	if err != nil {
		c.JSON(http.StatusNotFound, errorBody("NAME_UNKNOWN", "no registry serves this host"))
		return
	}
	// The /v2 surface is the OCI Distribution API only. Non-OCI registries are
	// served by the artifact dispatcher (NoRoute), never here.
	if registry.Format(reg.Format) != registry.FormatDocker {
		c.JSON(http.StatusNotFound, errorBody("MANIFEST_UNKNOWN", "registry does not speak the OCI API"))
		return
	}
	// Offline registries reject all access.
	if !reg.Online {
		c.JSON(http.StatusServiceUnavailable, errorBody("UNAVAILABLE", "registry is offline"))
		return
	}
	// Per-repository access control. The repository is extracted here so a grant
	// scoped to a path ("docker:kosmos/**") narrows to it; passing only the
	// registry would silently widen every such grant to the whole registry.
	if !h.allowRepo(c, reg, repoFromPath(path), isWriteMethod(c.Request.Method)) {
		h.denyAccess(c, "access denied to registry "+reg.Name)
		return
	}
	// Cache registries are read-only; proxy registries may refuse writes unless
	// write-through is enabled. Both reject anonymous writes earlier in Dispatch.
	if isWriteMethod(c.Request.Method) {
		switch registry.Type(reg.Type) {
		case registry.TypeCache:
			c.JSON(http.StatusMethodNotAllowed, errorBody("DENIED", "cache registry is read-only"))
			return
		case registry.TypeProxy:
			if !reg.ProxyAllowWrite {
				c.JSON(http.StatusForbidden, errorBody("DENIED", "proxy registry does not allow writes"))
				return
			}
		}
	}

	c.Set("registry", reg.Name)
	segs := strings.Split(path, "/")
	n := len(segs)

	switch {
	case n >= 2 && segs[n-2] == "manifests":
		h.manifestOp(c, be, segs[:n-2], segs[n-1])
	case n >= 2 && segs[n-2] == "blobs" && segs[n-1] == "uploads":
		h.uploadStart(c, be, segs[:n-2])
	case n >= 2 && segs[n-2] == "blobs":
		h.blobOp(c, be, segs[:n-2], segs[n-1])
	case n >= 3 && segs[n-3] == "blobs" && segs[n-2] == "uploads":
		h.uploadOp(c, be, segs[:n-3], segs[n-1])
	case n >= 2 && segs[n-2] == "tags" && segs[n-1] == "list":
		h.tagsList(c, be, segs[:n-2])
	default:
		c.JSON(http.StatusNotFound, errorBody("MANIFEST_UNKNOWN", "unknown endpoint"))
	}
}

// repoFrom joins the leading path segments into a repository name.
func repoFrom(segs []string) string { return strings.Join(segs, "/") }

// ---- Manifests ----

func (h *Handler) manifestOp(c *gin.Context, be registry.Backend, repoSegs []string, reference string) {
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		h.getManifest(c, be, repo, reference)
	case http.MethodHead:
		h.headManifest(c, be, repo, reference)
	case http.MethodPut:
		h.putManifest(c, be, repo, reference)
	case http.MethodDelete:
		h.deleteManifest(c, be, repo, reference)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) getManifest(c *gin.Context, be registry.Backend, repo, reference string) {
	content, mt, err := be.GetManifest(repo, reference)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("MANIFEST_UNKNOWN", "manifest not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	d := digest.FromBytes(content)
	c.Header("Docker-Content-Digest", d.String())
	c.Header("Content-Type", mt)
	c.Header("Content-Length", strconv.Itoa(len(content)))
	c.Data(http.StatusOK, mt, content)
}

func (h *Handler) headManifest(c *gin.Context, be registry.Backend, repo, reference string) {
	content, mt, err := be.GetManifest(repo, reference)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("MANIFEST_UNKNOWN", "manifest not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	d := digest.FromBytes(content)
	c.Header("Docker-Content-Digest", d.String())
	c.Header("Content-Type", mt)
	c.Header("Content-Length", strconv.Itoa(len(content)))
	c.Status(http.StatusOK)
}

func (h *Handler) putManifest(c *gin.Context, be registry.Backend, repo, reference string) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("BLOB_UPLOAD_INVALID", err.Error()))
		return
	}
	if refDigest, perr := digest.Parse(reference); perr == nil {
		if refDigest != digest.FromBytes(body) {
			c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "reference digest does not match content"))
			return
		}
	}
	mediaType := c.GetHeader("Content-Type")
	if mediaType == "" {
		mediaType = "application/vnd.oci.image.manifest.v1+json"
	}

	d, err := be.PutManifest(repo, reference, mediaType, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}

	if err := linkManifestBlobs(repo, d, body, be); err != nil {
		_ = err
	}

	loc := h.location(c, repo, "manifests/"+d.String())
	c.Header("Docker-Content-Digest", d.String())
	c.Header("Location", loc)
	c.Status(http.StatusCreated)
}

func (h *Handler) deleteManifest(c *gin.Context, be registry.Backend, repo, reference string) {
	if err := be.DeleteManifest(repo, reference); err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("MANIFEST_UNKNOWN", "manifest not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	c.Status(http.StatusAccepted)
}

// ---- Blobs ----

func (h *Handler) blobOp(c *gin.Context, be registry.Backend, repoSegs []string, dgst string) {
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	d, err := digest.Parse(dgst)
	if err != nil {
		c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "invalid digest"))
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		h.getBlob(c, be, repo, d)
	case http.MethodHead:
		h.headBlob(c, be, repo, d)
	case http.MethodDelete:
		h.deleteBlob(c, be, d)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) getBlob(c *gin.Context, be registry.Backend, repo string, d digest.Digest) {
	rc, size, err := be.GetBlob(repo, d)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("BLOB_UNKNOWN", "blob not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	defer rc.Close()
	c.Header("Docker-Content-Digest", d.String())
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
	io.Copy(c.Writer, rc)
}

func (h *Handler) headBlob(c *gin.Context, be registry.Backend, repo string, d digest.Digest) {
	size, err := be.StatBlob(repo, d)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("BLOB_UNKNOWN", "blob not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	c.Header("Docker-Content-Digest", d.String())
	c.Header("Content-Type", "application/octet-stream")
	c.Header("Content-Length", strconv.FormatInt(size, 10))
	c.Status(http.StatusOK)
}

func (h *Handler) deleteBlob(c *gin.Context, be registry.Backend, d digest.Digest) {
	if err := be.DeleteBlob(d); err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("BLOB_UNKNOWN", "blob not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	c.Status(http.StatusAccepted)
}

// ---- Uploads ----

func (h *Handler) uploadStart(c *gin.Context, be registry.Backend, repoSegs []string) {
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	switch c.Request.Method {
	case http.MethodPost:
		// Monolithic upload: digest supplied, body present.
		if dgstStr := c.Query("digest"); dgstStr != "" {
			d, err := digest.Parse(dgstStr)
			if err != nil {
				c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "invalid digest"))
				return
			}
			u, err := be.NewUpload(repo)
			if err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return
			}
			if _, err := be.AppendUpload(u, c.Request.Body); err != nil {
				_ = be.CancelUpload(u)
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return
			}
			if err := be.CommitUpload(u, d); err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return
			}
			loc := h.location(c, repo, "blobs/"+d.String())
			c.Header("Docker-Content-Digest", d.String())
			c.Header("Location", loc)
			c.Status(http.StatusCreated)
			return
		}
		// Chunked upload: start a session.
		u, err := be.NewUpload(repo)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		loc := h.location(c, repo, "blobs/uploads/"+u.ID)
		c.Header("Location", loc)
		c.Header("Docker-Upload-UUID", u.ID)
		c.Header("Range", "0-0")
		c.Status(http.StatusAccepted)
	case http.MethodGet:
		c.Status(http.StatusNotFound)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

func (h *Handler) uploadOp(c *gin.Context, be registry.Backend, repoSegs []string, uuid string) {
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	u, err := be.GetUpload(uuid)
	if err != nil {
		c.JSON(http.StatusNotFound, errorBody("BLOB_UPLOAD_UNKNOWN", "upload session not found"))
		return
	}
	switch c.Request.Method {
	case http.MethodGet:
		offset, err := be.UploadOffset(u)
		if err != nil {
			c.JSON(http.StatusNotFound, errorBody("BLOB_UPLOAD_UNKNOWN", "upload session not found"))
			return
		}
		c.Header("Docker-Upload-UUID", u.ID)
		c.Header("Range", "0-"+strconv.FormatInt(offset-1, 10))
		c.Status(http.StatusNoContent)
	case http.MethodPatch:
		offset, err := be.AppendUpload(u, c.Request.Body)
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		loc := h.location(c, repo, "blobs/uploads/"+u.ID)
		c.Header("Location", loc)
		c.Header("Docker-Upload-UUID", u.ID)
		c.Header("Range", "0-"+strconv.FormatInt(offset-1, 10))
		c.Status(http.StatusAccepted)
	case http.MethodPut:
		dgstStr := c.Query("digest")
		if dgstStr == "" {
			c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "missing digest query parameter"))
			return
		}
		d, err := digest.Parse(dgstStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, errorBody("DIGEST_INVALID", "invalid digest"))
			return
		}
		// The closing PUT may carry the final chunk of the blob (OCI spec,
		// "Pushing a blob in chunks"), and a POST-then-PUT monolithic push
		// sends the whole blob here. Without appending it first the session is
		// committed empty and the push fails with a digest mismatch.
		if c.Request.Body != nil {
			if _, err := be.AppendUpload(u, c.Request.Body); err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return
			}
		}
		if err := be.CommitUpload(u, d); err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		loc := h.location(c, repo, "blobs/"+d.String())
		c.Header("Docker-Content-Digest", d.String())
		c.Header("Location", loc)
		c.Status(http.StatusCreated)
	case http.MethodDelete:
		if err := be.CancelUpload(u); err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		c.Status(http.StatusNoContent)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
}

// ---- Tags ----

func (h *Handler) tagsList(c *gin.Context, be registry.Backend, repoSegs []string) {
	repo := repoFrom(repoSegs)
	if !validRepo(repo) {
		c.JSON(http.StatusBadRequest, errorBody("NAME_INVALID", "invalid repository name"))
		return
	}
	tags, err := be.ListTags(repo)
	if err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	if tags == nil {
		tags = []string{}
	}
	c.JSON(http.StatusOK, gin.H{
		"name": repo,
		"tags": tags,
	})
}

// ---- Helpers ----

// repoFromPath extracts the repository name from an OCI path. Names contain
// slashes and have no fixed depth, so the split is on the operation that
// follows them rather than on a segment count.
func repoFromPath(path string) string {
	for _, sep := range []string{"/manifests/", "/blobs/", "/tags/"} {
		if i := strings.LastIndex(path, sep); i > 0 {
			return path[:i]
		}
	}
	return ""
}

func (h *Handler) location(c *gin.Context, repo, suffix string) string {
	// Every OCI push follows this Location header, so the scheme has to be the
	// one the client actually used — see auth.RequestScheme.
	return auth.RequestScheme(c) + "://" + c.Request.Host + "/v2/" + repo + "/" + suffix
}

func validRepo(name string) bool {
	if name == "" {
		return false
	}
	return repoNameRegex.MatchString(name)
}

type errorBodyT struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func errorBody(code, msg string) errorBodyT {
	return errorBodyT{Code: code, Message: msg}
}

// linkManifestBlobs records references from a manifest's config and layers so
// that the underlying blobs cannot be deleted while still referenced.
func linkManifestBlobs(repo string, manifest digest.Digest, content []byte, be registry.Backend) error {
	var m struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(content, &m); err != nil {
		return nil
	}
	digests := map[digest.Digest]bool{}
	if m.Config.Digest != "" {
		if d, err := digest.Parse(m.Config.Digest); err == nil {
			digests[d] = true
		}
	}
	for _, l := range m.Layers {
		if l.Digest != "" {
			if d, err := digest.Parse(l.Digest); err == nil {
				digests[d] = true
			}
		}
	}
	for d := range digests {
		if err := be.LinkManifestBlob(repo, manifest, d); err != nil {
			return err
		}
	}
	return nil
}
