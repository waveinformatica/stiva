package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/storage"
)

const (
	maxPackageList     = 1000
	maxPackageVersions = 200
)

// objectInfosFor lists a path-addressed registry's stored files with sizes and
// times when the registry has a store, and bare paths (groups) otherwise.
func (h *Handler) objectInfosFor(reg *registry.Registry) ([]storage.ObjectInfo, error) {
	if st := h.mgr.StoreFor(reg.Name); st != nil {
		return st.ListObjectInfos("")
	}
	be, err := h.mgr.ArtifactBackendFor(reg.Name)
	if err != nil {
		return nil, err
	}
	defer be.Close()
	paths, err := be.List("")
	if err != nil {
		return nil, err
	}
	out := make([]storage.ObjectInfo, len(paths))
	for i, p := range paths {
		out[i] = storage.ObjectInfo{Path: p}
	}
	return out, nil
}

// packageRegistry resolves ?registry= to a readable path-addressed registry,
// answering the error itself when it is not one.
func (h *Handler) packageRegistry(c *gin.Context) (*registry.Registry, bool) {
	reg, ok := h.mgr.Get(h.registryParam(c))
	if !ok || !h.allowAccess(c, reg, false) {
		c.JSON(http.StatusNotFound, gin.H{"error": "registry not found"})
		return nil, false
	}
	if registry.Format(reg.Format) == registry.FormatDocker {
		c.JSON(http.StatusBadRequest, gin.H{"error": "images are browsed by repository"})
		return nil, false
	}
	return reg, true
}

type packageRow struct {
	Name     string `json:"name"`
	Latest   string `json:"latest"`
	Versions int    `json:"versions"`
	Files    int    `json:"files"`
	Size     int64  `json:"size"`
	Updated  int64  `json:"updated_at"` // ms
}

// uiPackages serves GET /api/v1/packages?registry=&q=: the packages of a
// registry with their newest version, for the browser.
func (h *Handler) uiPackages(c *gin.Context) {
	reg, ok := h.packageRegistry(c)
	if !ok {
		return
	}
	infos, err := h.objectInfosFor(reg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	needle := strings.ToLower(strings.TrimSpace(c.Query("q")))
	names, byPkg := groupPackages(registry.Format(reg.Format), infos)
	rows := []packageRow{}
	for _, n := range names {
		if needle != "" && !strings.Contains(strings.ToLower(n), needle) {
			continue
		}
		us := byPkg[n]
		r := packageRow{Name: n, Latest: us[0].Version, Versions: len(us)}
		for _, u := range us {
			r.Files += len(u.Paths)
			r.Size += u.Size
			if ms := u.CreatedAt / 1e6; ms > r.Updated {
				r.Updated = ms
			}
		}
		rows = append(rows, r)
	}
	total := len(rows)
	if len(rows) > maxPackageList {
		rows = rows[:maxPackageList]
	}
	c.JSON(http.StatusOK, gin.H{"packages": rows, "total": total, "format": reg.Format, "endpoint": h.endpointURL(c, reg)})
}

type versionDetail struct {
	versionView
	Usage []snippet `json:"usage,omitempty"`
}

// uiPackage serves GET /api/v1/package?registry=&name=: every version of one
// package with its files, and how to use it.
func (h *Handler) uiPackage(c *gin.Context) {
	reg, ok := h.packageRegistry(c)
	if !ok {
		return
	}
	name := c.Query("name")
	infos, err := h.objectInfosFor(reg)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	format := registry.Format(reg.Format)
	_, byPkg := groupPackages(format, infos)
	us, found := byPkg[name]
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": "package not found"})
		return
	}
	sizes := map[string]int64{}
	for _, o := range infos {
		sizes[o.Path] = o.Size
	}
	ep := h.endpointURL(c, reg)
	versions := make([]versionDetail, 0, len(us))
	for i, u := range us {
		if i >= maxPackageVersions {
			break
		}
		v := versionOf(u, sizes, "")
		versions = append(versions, versionDetail{versionView: v, Usage: useSnippets(format, ep, name, v.Version, v.Primary)})
	}
	c.JSON(http.StatusOK, gin.H{
		"name": name, "format": reg.Format, "endpoint": ep,
		"setup": setupSnippets(format, ep), "versions": versions, "total_versions": len(us),
	})
}
