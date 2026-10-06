package api

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/auth"
	"registry/internal/registry"
	"registry/internal/storage"
)

// ArtifactDispatch serves non-OCI artifact formats (helm/maven/npm). It is called
// from the engine NoRoute so it coexists with the /v2 OCI routes and the static
// UI. It returns false when the request is not for an artifact registry, letting
// the caller fall back to the static UI.
func (h *Handler) ArtifactDispatch(c *gin.Context) bool {
	reg, be, p, ok := h.resolveArtifact(c)
	if !ok {
		return false
	}
	if registry.Format(reg.Format) == registry.FormatDocker {
		return false
	}
	if !reg.Online {
		c.JSON(http.StatusServiceUnavailable, errorBody("UNAVAILABLE", "registry is offline"))
		return true
	}

	// Writes require authentication and are forbidden for cache registries.
	if isWriteMethod(c.Request.Method) {
		if u, ok := c.Get("user"); ok {
			if usr, ok2 := u.(*auth.User); ok2 && usr.IsAnonymous() {
				c.Header("WWW-Authenticate", h.authMgr.Challenge(c))
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"errors": []gin.H{{"code": "DENIED", "message": "authentication required to push"}},
				})
				return true
			}
		}
		switch registry.Type(reg.Type) {
		case registry.TypeCache:
			c.JSON(http.StatusMethodNotAllowed, errorBody("DENIED", "cache registry is read-only"))
			return true
		case registry.TypeProxy:
			if !reg.ProxyAllowWrite {
				c.JSON(http.StatusForbidden, errorBody("DENIED", "proxy registry does not allow writes"))
				return true
			}
		}
	}

	// Per-repository access control (read or write). The anonymous-write 401
	// above still fires first for unauthenticated pushes; this enforces the
	// explicit permission rules for authenticated principals.
	if !h.allowAccess(c, reg, isWriteMethod(c.Request.Method)) {
		h.denyAccess(c, "access denied to registry "+reg.Name)
		return true
	}

	switch c.Request.Method {
	case http.MethodGet, http.MethodHead:
		h.artifactGet(c, be, reg, p)
	case http.MethodPut, http.MethodPost:
		h.artifactPut(c, be, reg, p)
	case http.MethodDelete:
		h.artifactDelete(c, be, p)
	default:
		c.Status(http.StatusMethodNotAllowed)
	}
	return true
}

// IsArtifactHost reports whether this request targets a non-OCI artifact
// registry. The catch-all route uses it to decide whether authentication is
// required: artifact paths are protected, but the static web UI must be
// reachable unauthenticated. Serving the SPA only to authenticated callers is
// a deadlock — the SPA *is* the login form, and a 401 carrying
// WWW-Authenticate: Bearer produces no browser login prompt.
func (h *Handler) IsArtifactHost(c *gin.Context) bool {
	reg, _, _, ok := h.resolveArtifact(c)
	if !ok {
		return false
	}
	return registry.Format(reg.Format) != registry.FormatDocker
}

func (h *Handler) resolveArtifact(c *gin.Context) (*registry.Registry, registry.ArtifactBackend, string, bool) {
	pinnedName := ""
	if v := c.Request.Context().Value(registry.PinnedContextKey); v != nil {
		if s, ok := v.(string); ok {
			pinnedName = s
		}
	}
	reg, rel, err := h.mgr.Resolve(c.Request.Host, pinnedName, strings.Trim(c.Request.URL.Path, "/"))
	if err != nil {
		return nil, nil, "", false
	}
	be, err := h.mgr.ArtifactBackendFor(reg.Name)
	if err != nil {
		return nil, nil, "", false
	}
	return reg, be, rel, true
}

func (h *Handler) artifactGet(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry, p string) {
	// Hosted registries synthesize format-specific metadata documents.
	if registry.Type(reg.Type) == registry.TypeHosted {
		if handled := h.maybeGenerateMetadata(c, be, reg, p); handled {
			return
		}
	}

	if p == "" {
		objs, err := be.List("")
		if err != nil {
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		c.JSON(http.StatusOK, gin.H{"objects": objs})
		return
	}

	if c.Request.Method == http.MethodHead {
		sz, ct, err := be.Head(p)
		if err != nil {
			if err == storage.ErrNotFound {
				c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "object not found"))
				return
			}
			c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
			return
		}
		c.Header("Content-Type", ct)
		c.Header("Content-Length", strconv.FormatInt(sz, 10))
		c.Status(http.StatusOK)
		return
	}

	rc, sz, ct, err := be.Get(p)
	if err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "object not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	defer rc.Close()
	if ct == "" {
		ct = registry.ContentTypeForPath(p)
	}
	c.Header("Content-Type", ct)
	c.Header("Content-Length", strconv.FormatInt(sz, 10))
	c.Status(http.StatusOK)
	io.Copy(c.Writer, rc)
}

func (h *Handler) artifactPut(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry, p string) {
	if p == "" {
		c.JSON(http.StatusBadRequest, errorBody("INVALID", "object path is required"))
		return
	}
	ct := c.GetHeader("Content-Type")
	if ct == "" || ct == "application/octet-stream" {
		ct = registry.ContentTypeForPath(p)
	}
	if err := be.Put(p, ct, c.Request.Body); err != nil {
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	c.Status(http.StatusCreated)
}

func (h *Handler) artifactDelete(c *gin.Context, be registry.ArtifactBackend, p string) {
	if p == "" {
		c.JSON(http.StatusBadRequest, errorBody("INVALID", "object path is required"))
		return
	}
	if err := be.Delete(p); err != nil {
		if err == storage.ErrNotFound {
			c.JSON(http.StatusNotFound, errorBody("NOT_FOUND", "object not found"))
			return
		}
		c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
		return
	}
	c.Status(http.StatusAccepted)
}

// maybeGenerateMetadata handles format-specific metadata documents for hosted
// registries. It returns true when it produced a response.
func (h *Handler) maybeGenerateMetadata(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry, p string) bool {
	if registry.Format(reg.Format) == registry.FormatAPT && registry.Type(reg.Type) == registry.TypeHosted {
		if body, ct, handled := h.aptSignedMetadata(c, be, reg, p); handled {
			c.Header("Content-Type", ct)
			c.Header("Content-Length", strconv.Itoa(len(body)))
			c.Status(http.StatusOK)
			c.Writer.Write(body)
			return true
		}
	}
	switch registry.Format(reg.Format) {
	case registry.FormatHelm:
		if p == "index.yaml" {
			body, err := generateHelmIndex(be)
			if err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return true
			}
			c.Header("Content-Type", "application/yaml")
			c.Header("Content-Length", strconv.Itoa(len(body)))
			c.Status(http.StatusOK)
			c.Writer.Write(body)
			return true
		}
		return false
	case registry.FormatMaven:
		if strings.HasSuffix(p, "/maven-metadata.xml") {
			body, err := generateMavenMetadata(be, p)
			if err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return true
			}
			c.Header("Content-Type", "application/xml")
			c.Header("Content-Length", strconv.Itoa(len(body)))
			c.Status(http.StatusOK)
			c.Writer.Write(body)
			return true
		}
		return false
	case registry.FormatNpm:
		if isNpmMetaPath(p) {
			body, err := generateNpmMetadata(be, p)
			if err != nil {
				c.JSON(http.StatusInternalServerError, errorBody("INTERNAL_ERROR", err.Error()))
				return true
			}
			c.Header("Content-Type", "application/json")
			c.Header("Content-Length", strconv.Itoa(len(body)))
			c.Status(http.StatusOK)
			c.Writer.Write(body)
			return true
		}
		return false
	}

	// Generic generators for the additional package-manager formats.
	if body, ct, handled := generateMetadataFor(be, registry.Format(reg.Format), p); handled {
		c.Header("Content-Type", ct)
		c.Header("Content-Length", strconv.Itoa(len(body)))
		c.Status(http.StatusOK)
		c.Writer.Write(body)
		return true
	}
	return false
}

// isNpmMetaPath reports whether p is an npm package metadata path (not a tarball
// or a search endpoint).
func isNpmMetaPath(p string) bool {
	if p == "" || strings.HasSuffix(p, ".tgz") || strings.Contains(p, "/-/") {
		return false
	}
	if p == "-" || strings.HasPrefix(p, "-/") {
		return false
	}
	return true
}

// ---- Format-specific metadata generation (hosted registries) ----

func generateHelmIndex(be registry.ArtifactBackend) ([]byte, error) {
	objs, err := be.List("")
	if err != nil {
		return nil, err
	}
	entries := map[string][]string{}
	var names []string
	for _, o := range objs {
		if !strings.HasSuffix(o, ".tgz") {
			continue
		}
		base := strings.TrimSuffix(path.Base(o), ".tgz")
		i := strings.LastIndex(base, "-")
		if i < 0 {
			continue
		}
		name := base[:i]
		version := base[i+1:]
		if name == "" || version == "" {
			continue
		}
		if _, ok := entries[name]; !ok {
			names = append(names, name)
		}
		entries[name] = append(entries[name], "  - name: "+name+"\n    version: "+version+"\n    urls:\n      - "+o)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("apiVersion: v1\n")
	b.WriteString("entries:\n")
	for _, n := range names {
		b.WriteString("  " + n + ":\n")
		for _, e := range entries[n] {
			b.WriteString(e + "\n")
		}
	}
	return []byte(b.String()), nil
}

func generateMavenMetadata(be registry.ArtifactBackend, p string) ([]byte, error) {
	prefix := strings.TrimSuffix(p, "maven-metadata.xml")
	objs, err := be.List(prefix)
	if err != nil {
		return nil, err
	}
	artifact := path.Base(strings.TrimRight(prefix, "/"))
	groupId := strings.ReplaceAll(strings.TrimRight(prefix, "/"), "/", ".")
	versions := map[string]struct{}{}
	for _, o := range objs {
		base := path.Base(o)
		ext := ""
		for _, e := range []string{".jar", ".pom", ".war", ".ear"} {
			if strings.HasSuffix(base, e) {
				ext = e
				break
			}
		}
		if ext == "" || !strings.HasPrefix(base, artifact+"-") {
			continue
		}
		v := strings.TrimSuffix(base[len(artifact)+1:], ext)
		if v == "" {
			continue
		}
		versions[v] = struct{}{}
	}
	var vs []string
	for v := range versions {
		vs = append(vs, v)
	}
	sort.Strings(vs)
	latest := ""
	release := ""
	if len(vs) > 0 {
		latest = vs[len(vs)-1]
		release = vs[len(vs)-1]
	}
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	b.WriteString("<metadata>\n")
	b.WriteString("  <groupId>" + xmlEscape(groupId) + "</groupId>\n")
	b.WriteString("  <artifactId>" + xmlEscape(artifact) + "</artifactId>\n")
	b.WriteString("  <versioning>\n")
	b.WriteString("    <versions>\n")
	for _, v := range vs {
		b.WriteString("      <version>" + xmlEscape(v) + "</version>\n")
	}
	b.WriteString("    </versions>\n")
	b.WriteString("    <latest>" + xmlEscape(latest) + "</latest>\n")
	b.WriteString("    <release>" + xmlEscape(release) + "</release>\n")
	b.WriteString("  </versioning>\n")
	b.WriteString("</metadata>\n")
	return []byte(b.String()), nil
}

func generateNpmMetadata(be registry.ArtifactBackend, pkg string) ([]byte, error) {
	prefix := pkg + "/-/"
	objs, err := be.List(prefix)
	if err != nil {
		return nil, err
	}
	versions := map[string]json.RawMessage{}
	latest := ""
	for _, o := range objs {
		if !strings.HasSuffix(o, ".tgz") {
			continue
		}
		rc, _, _, err := be.Get(o)
		if err != nil {
			continue
		}
		pkgJSON, err := extractPackageJSON(rc)
		rc.Close()
		if err != nil {
			continue
		}
		ver, _ := pkgJSON["version"].(string)
		if ver == "" {
			continue
		}
		raw, _ := json.Marshal(pkgJSON)
		versions[ver] = raw
		if ver > latest {
			latest = ver
		}
	}
	doc := map[string]interface{}{
		"name":        pkg,
		"versions":    versions,
		"dist-tags":   map[string]string{"latest": latest},
		"_attachment": len(versions),
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return body, nil
}

// extractPackageJSON reads a gzipped tarball and returns the package.json found
// under the package/ directory.
func extractPackageJSON(r io.Reader) (map[string]interface{}, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Name != "package/package.json" && !strings.HasSuffix(hdr.Name, "/package.json") {
			continue
		}
		var pkg map[string]interface{}
		if err := json.NewDecoder(tr).Decode(&pkg); err != nil {
			return nil, err
		}
		return pkg, nil
	}
	return nil, fmt.Errorf("package.json not found in tarball")
}

func xmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;", "'", "&apos;")
	return r.Replace(s)
}
