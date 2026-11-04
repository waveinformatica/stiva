package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"registry/internal/registry"
	"registry/internal/storage"
)

// The application catalog is the home page: a curated, app-store style list of
// the things users come here for. Each entry points at artifacts in the
// registries, and an entry is shown to a caller only if at least one of them is
// readable by that caller; the artifacts they cannot read are left out of the
// entry. The catalog adds no access of its own, it only reflects the grants.

// SetCatalog wires the catalog store.
func (h *Handler) SetCatalog(s storage.CatalogStore) { h.catalog = s }

func (h *Handler) catalogReady(c *gin.Context) bool {
	if h.catalog == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "catalog not configured"})
		return false
	}
	return true
}

// Limits keep a catalog entry a card, not a data store.
const (
	maxIconBytes     = 96 << 10
	maxDescription   = 20000
	maxCatalogArtefs = 50
	maxCatalogFiles  = 100
	recentTags       = 6
)

var (
	slugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	iconDataRe = regexp.MustCompile(`^data:image/(png|jpeg|webp|gif);base64,([A-Za-z0-9+/=]+)$`)
)

// validateCatalogApp checks an entry before it is saved. exists tells whether a
// registry name is defined.
func validateCatalogApp(a *storage.CatalogApp, exists func(registry string) bool) error {
	a.Slug = strings.TrimSpace(a.Slug)
	a.Name = strings.TrimSpace(a.Name)
	switch {
	case !slugRe.MatchString(a.Slug):
		return errors.New("slug must be lowercase letters, digits and dashes (it becomes the page address)")
	case a.Name == "" || len(a.Name) > 100:
		return errors.New("name is required (at most 100 characters)")
	case len(a.Tagline) > 200:
		return errors.New("tagline must be at most 200 characters")
	case len(a.Description) > maxDescription:
		return errors.New("description is too long")
	case len(a.Category) > 40:
		return errors.New("category must be at most 40 characters")
	case len(a.Artifacts) > maxCatalogArtefs:
		return errors.New("too many artifacts")
	}
	if a.Homepage != "" {
		u, err := url.Parse(a.Homepage)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("homepage must be an http(s) URL")
		}
	}
	if a.Icon != "" {
		switch {
		case strings.HasPrefix(a.Icon, "data:"):
			m := iconDataRe.FindStringSubmatch(a.Icon)
			if m == nil {
				return errors.New("icon must be a png, jpeg, webp or gif image")
			}
			raw, err := base64.StdEncoding.DecodeString(m[2])
			if err != nil || len(raw) > maxIconBytes {
				return errors.New("icon is invalid or larger than 96 KB")
			}
		default:
			u, err := url.Parse(a.Icon)
			if err != nil || u.Scheme != "https" || u.Host == "" {
				return errors.New("icon must be an https URL or an uploaded image")
			}
		}
	}
	for i := range a.Artifacts {
		ar := &a.Artifacts[i]
		ar.Ref = strings.Trim(strings.TrimSpace(ar.Ref), "/")
		if ar.Ref == "" {
			return errors.New("every artifact needs a repository or path")
		}
		if !exists(ar.Registry) {
			return errors.New("artifact refers to unknown registry " + ar.Registry)
		}
		if len(ar.Tag) > 128 || len(ar.Label) > 100 {
			return errors.New("artifact tag or label is too long")
		}
	}
	return nil
}

// visibleArtifacts returns the artifacts of a that pass the check, in order.
// An app with none is not shown at all.
func visibleArtifacts(a storage.CatalogApp, visible func(storage.CatalogArtifact) bool) []storage.CatalogArtifact {
	var out []storage.CatalogArtifact
	for _, ar := range a.Artifacts {
		if visible(ar) {
			out = append(out, ar)
		}
	}
	return out
}

// artifactVisible reports whether the caller can read what ar points at: the
// registry is defined and online, access is granted (per repository for OCI, per
// registry for path-addressed formats, as the serving code enforces it), and
// the content exists.
func (h *Handler) artifactVisible(c *gin.Context, ar storage.CatalogArtifact) (*registry.Registry, bool) {
	reg, ok := h.mgr.Get(ar.Registry)
	if !ok || !reg.Online {
		return nil, false
	}
	if registry.Format(reg.Format) == registry.FormatDocker {
		if !h.allowRepo(c, reg, ar.Ref, false) {
			return nil, false
		}
		be, err := h.mgr.BackendFor(reg.Name)
		if err != nil {
			return nil, false
		}
		tags, err := be.ListTags(ar.Ref)
		if err != nil || len(tags) == 0 {
			return nil, false
		}
		if ar.Tag != "" {
			found := false
			for _, t := range tags {
				found = found || t == ar.Tag
			}
			return reg, found
		}
		return reg, true
	}
	if !h.allowAccess(c, reg, false) {
		return nil, false
	}
	be, err := h.mgr.ArtifactBackendFor(reg.Name)
	if err != nil {
		return nil, false
	}
	defer be.Close()
	paths, err := be.List(ar.Ref)
	return reg, err == nil && len(paths) > 0
}

type catalogCard struct {
	Slug      string   `json:"slug"`
	Name      string   `json:"name"`
	Tagline   string   `json:"tagline"`
	Category  string   `json:"category"`
	Icon      string   `json:"icon"`
	Featured  bool     `json:"featured"`
	Formats   []string `json:"formats"`
	Artifacts int      `json:"artifacts"`
}

// catalogList serves GET /api/v1/catalog: the apps the caller may see.
func (h *Handler) catalogList(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	apps, err := h.catalog.ListCatalogApps(true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	cards := []catalogCard{}
	cats := map[string]bool{}
	for _, a := range apps {
		formats := map[string]bool{}
		vis := visibleArtifacts(a, func(ar storage.CatalogArtifact) bool {
			reg, ok := h.artifactVisible(c, ar)
			if ok {
				formats[reg.Format] = true
			}
			return ok
		})
		if len(vis) == 0 {
			continue
		}
		fs := make([]string, 0, len(formats))
		for f := range formats {
			fs = append(fs, f)
		}
		sort.Strings(fs)
		cards = append(cards, catalogCard{
			Slug: a.Slug, Name: a.Name, Tagline: a.Tagline, Category: a.Category, Icon: a.Icon,
			Featured: a.Featured, Formats: fs, Artifacts: len(vis),
		})
		if a.Category != "" {
			cats[a.Category] = true
		}
	}
	categories := make([]string, 0, len(cats))
	for k := range cats {
		categories = append(categories, k)
	}
	sort.Strings(categories)
	c.JSON(http.StatusOK, gin.H{"apps": cards, "categories": categories})
}

type tagView struct {
	Tag      string `json:"tag"`
	Digest   string `json:"digest"`
	PushedAt int64  `json:"pushed_at"`
}

type fileView struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type artifactView struct {
	Registry  string     `json:"registry"`
	Format    string     `json:"format"`
	Type      string     `json:"type"`
	Ref       string     `json:"ref"`
	Tag       string     `json:"tag"`
	Label     string     `json:"label"`
	Kind      string     `json:"kind"` // "image" | "files"
	PullHost  string     `json:"pull_host,omitempty"`
	Tags      []tagView  `json:"tags,omitempty"`
	Files     []fileView `json:"files,omitempty"`
	MoreFiles int        `json:"more_files,omitempty"`
}

func (h *Handler) describeArtifact(c *gin.Context, reg *registry.Registry, ar storage.CatalogArtifact) artifactView {
	v := artifactView{Registry: reg.Name, Format: reg.Format, Type: reg.Type, Ref: ar.Ref, Tag: ar.Tag, Label: ar.Label}
	if registry.Format(reg.Format) == registry.FormatDocker {
		v.Kind = "image"
		v.PullHost, _, _ = h.pullRoute(c.Request.Host, reg)
		if st := h.mgr.StoreFor(reg.Name); st != nil {
			if infos, err := st.ListTagInfos(ar.Ref); err == nil {
				sort.Slice(infos, func(i, j int) bool { return infos[i].PushedAt > infos[j].PushedAt })
				for _, t := range infos {
					if ar.Tag != "" && t.Tag != ar.Tag {
						continue
					}
					if len(v.Tags) < recentTags {
						v.Tags = append(v.Tags, tagView{t.Tag, t.Digest, t.PushedAt})
					}
				}
				return v
			}
		}
		if be, err := h.mgr.BackendFor(reg.Name); err == nil {
			if tags, err := be.ListTags(ar.Ref); err == nil {
				sort.Strings(tags)
				for _, t := range tags {
					if ar.Tag != "" && t != ar.Tag {
						continue
					}
					if len(v.Tags) < recentTags {
						v.Tags = append(v.Tags, tagView{Tag: t})
					}
				}
			}
		}
		return v
	}
	v.Kind = "files"
	be, err := h.mgr.ArtifactBackendFor(reg.Name)
	if err != nil {
		return v
	}
	defer be.Close()
	sizes := map[string]int64{}
	if l, ok := be.(objectLister); ok {
		if infos, err := l.ListObjectInfos(ar.Ref); err == nil {
			for _, o := range infos {
				sizes[o.Path] = o.Size
			}
		}
	}
	paths, _ := be.List(ar.Ref)
	sort.Strings(paths)
	for i, p := range paths {
		if i >= maxCatalogFiles {
			v.MoreFiles = len(paths) - maxCatalogFiles
			break
		}
		v.Files = append(v.Files, fileView{Path: p, Size: sizes[p]})
	}
	return v
}

// catalogGet serves GET /api/v1/catalog/:slug.
func (h *Handler) catalogGet(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	a, err := h.catalog.GetCatalogApp(c.Param("slug"))
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var views []artifactView
	for _, ar := range a.Artifacts {
		if reg, ok := h.artifactVisible(c, ar); ok {
			views = append(views, h.describeArtifact(c, reg, ar))
		}
	}
	// Same answer as for an unknown app: a hidden entry is not revealed.
	if len(views) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"slug": a.Slug, "name": a.Name, "tagline": a.Tagline, "description": a.Description,
		"icon": a.Icon, "category": a.Category, "homepage": a.Homepage, "featured": a.Featured,
		"artifacts": views,
	})
}

// ---- administration ----

func (h *Handler) adminListCatalog(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	apps, err := h.catalog.ListCatalogApps(true)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"apps": apps})
}

func (h *Handler) adminSaveCatalogApp(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	var a storage.CatalogApp
	if err := c.ShouldBindJSON(&a); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a.ID = 0
	if c.Param("id") != "" {
		id, ok := pathID(c, "id")
		if !ok {
			return
		}
		a.ID = id
	}
	exists := func(name string) bool { _, ok := h.mgr.Get(name); return ok }
	if err := validateCatalogApp(&a, exists); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	id, err := h.catalog.UpsertCatalogApp(a)
	if errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "could not save (is the slug already used?): " + err.Error()})
		return
	}
	saved, _ := h.catalog.GetCatalogAppByID(id)
	c.JSON(http.StatusOK, saved)
}

func (h *Handler) adminDeleteCatalogApp(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	if err := h.catalog.DeleteCatalogApp(id); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
