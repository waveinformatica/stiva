package api

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"path"
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
	maxShotBytes     = 512 << 10
	maxShots         = 8
	maxPackages      = 20
	otherVersions    = 50
	maxAllFiles      = 2000
	maxTags          = 200
)

var (
	slugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	iconDataRe = regexp.MustCompile(`^data:image/(png|jpeg|webp|gif);base64,([A-Za-z0-9+/=]+)$`)
)

// validateCatalogApp checks an entry before it is saved. formatOf reports the
// format of a defined registry.
func validateCatalogApp(a *storage.CatalogApp, formatOf func(registry string) (format string, ok bool)) error {
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
		format, known := formatOf(ar.Registry)
		if !known {
			return errors.New("artifact refers to unknown registry " + ar.Registry)
		}
		// A trailing slash marks a folder of a path-addressed registry ("builds/"
		// matches what is inside it, not "builds-old/"); an image repository has none.
		ar.Ref = strings.TrimLeft(strings.TrimSpace(ar.Ref), "/")
		if format == string(registry.FormatDocker) || !strings.HasSuffix(ar.Ref, "/") {
			ar.Ref = strings.TrimRight(ar.Ref, "/")
		} else {
			ar.Ref = strings.TrimRight(ar.Ref, "/") + "/"
		}
		if strings.Trim(ar.Ref, "/") == "" {
			return errors.New("every artifact needs a repository or path")
		}
		if len(ar.Tag) > 128 || len(ar.Label) > 100 {
			return errors.New("artifact tag or label is too long")
		}
		ar.Primary = strings.TrimSpace(ar.Primary)
		if len(ar.Primary) > 200 {
			return errors.New("main file pattern is too long")
		}
		if _, err := path.Match(ar.Primary, ""); err != nil {
			return errors.New("main file pattern is not a valid glob (use * and ?, e.g. *-linux-amd64.tar.gz)")
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
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Created int64  `json:"created_at,omitempty"` // ms since epoch
}

type artifactView struct {
	Registry string    `json:"registry"`
	Format   string    `json:"format"`
	Type     string    `json:"type"`
	Ref      string    `json:"ref"`
	Tag      string    `json:"tag"`
	Label    string    `json:"label"`
	Kind     string    `json:"kind"` // "image" | "files"
	PullHost string    `json:"pull_host,omitempty"`
	Tags     []tagView `json:"tags,omitempty"`

	// LatestTag is the tag to pull for an image: the pinned one, else the
	// highest stable version, else "latest", else the most recent.
	LatestTag string `json:"latest_tag,omitempty"`
	// Packages groups a file artifact's content by package, newest version first.
	Packages []packageView `json:"packages,omitempty"`
	// AllFiles is every file the caller can read under the artifact, so the
	// page can list them all; TotalFiles says how many exist beyond the cap.
	AllFiles   []fileView `json:"all_files,omitempty"`
	TotalFiles int        `json:"total_files,omitempty"`

	// Endpoint is the registry address clients use; Setup says how to point a
	// client at it (or, for an image, how to run it).
	Endpoint string    `json:"endpoint,omitempty"`
	Setup    []snippet `json:"setup,omitempty"`
}

type versionView struct {
	Version string     `json:"version"`
	Created int64      `json:"created_at"`
	Primary string     `json:"primary"` // the file to download for this version
	Files   []fileView `json:"files"`
}

type packageView struct {
	Name   string        `json:"name"`
	Latest versionView   `json:"latest"`
	Others []versionView `json:"others,omitempty"`
	// Usage is how to depend on the latest version; Download fetches its main file.
	Usage    []snippet `json:"usage,omitempty"`
	Download []snippet `json:"download,omitempty"`
	// Unversioned marks a package made of plain files with no version in their
	// names (a manifest.json): there is no "latest version" to speak of.
	Unversioned bool `json:"unversioned,omitempty"`
}

func (h *Handler) describeArtifact(c *gin.Context, reg *registry.Registry, ar storage.CatalogArtifact) artifactView {
	v := artifactView{Registry: reg.Name, Format: reg.Format, Type: reg.Type, Ref: ar.Ref, Tag: ar.Tag, Label: ar.Label}
	if registry.Format(reg.Format) == registry.FormatDocker {
		v.Kind = "image"
		v.PullHost, _, _ = h.pullRoute(c.Request.Host, reg)
		var all []tagView
		if st := h.mgr.StoreFor(reg.Name); st != nil {
			if infos, err := st.ListTagInfos(ar.Ref); err == nil {
				for _, t := range infos {
					all = append(all, tagView{t.Tag, t.Digest, t.PushedAt})
				}
			}
		}
		if all == nil {
			if be, err := h.mgr.BackendFor(reg.Name); err == nil {
				if tags, err := be.ListTags(ar.Ref); err == nil {
					for _, t := range tags {
						all = append(all, tagView{Tag: t})
					}
				}
			}
		}
		if ar.Tag != "" {
			var pinned []tagView
			for _, t := range all {
				if t.Tag == ar.Tag {
					pinned = append(pinned, t)
				}
			}
			all = pinned
			v.LatestTag = ar.Tag
		} else {
			v.LatestTag = pickLatestTag(all)
		}
		v.Setup = ociSnippets(v.PullHost, ar.Ref, v.LatestTag)
		sort.SliceStable(all, func(i, j int) bool {
			if all[i].PushedAt != all[j].PushedAt {
				return all[i].PushedAt > all[j].PushedAt
			}
			return all[i].Tag < all[j].Tag
		})
		if len(all) > maxTags {
			all = all[:maxTags]
		}
		v.Tags = all
		return v
	}

	v.Kind = "files"
	be, err := h.mgr.ArtifactBackendFor(reg.Name)
	if err != nil {
		return v
	}
	defer be.Close()
	var infos []storage.ObjectInfo
	if l, ok := be.(objectLister); ok {
		infos, _ = l.ListObjectInfos(ar.Ref)
	}
	if len(infos) == 0 {
		// No sizes or times to group by: plain paths.
		paths, _ := be.List(ar.Ref)
		for _, p := range paths {
			infos = append(infos, storage.ObjectInfo{Path: p})
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Path < infos[j].Path })
	v.TotalFiles = len(infos)
	for i, o := range infos {
		if i >= maxAllFiles {
			break
		}
		v.AllFiles = append(v.AllFiles, fileView{Path: o.Path, Size: o.Size, Created: o.CreatedAt / 1e6})
	}
	v.Packages = buildPackages(registry.Format(reg.Format), infos, ar.Primary)
	v.Endpoint = h.endpointURL(c, reg)
	v.Setup = setupSnippets(registry.Format(reg.Format), v.Endpoint)
	for i := range v.Packages {
		p := &v.Packages[i]
		p.Usage = useSnippets(registry.Format(reg.Format), v.Endpoint, p.Name, p.Latest.Version, p.Latest.Primary)
		// A format with no client syntax of its own already gets curl and wget as
		// its usage; do not show them twice.
		if useSnippets(registry.Format(reg.Format), v.Endpoint, "pkg", "1", "") != nil {
			p.Download = downloadSnippets(v.Endpoint, p.Latest.Primary)
		}
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
		"artifacts": views, "screenshots": a.Screenshots,
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
	formatOf := func(name string) (string, bool) {
		r, ok := h.mgr.Get(name)
		if !ok {
			return "", false
		}
		return r.Format, true
	}
	if err := validateCatalogApp(&a, formatOf); err != nil {
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

// ---- latest versions ----

var companionSuffix = []string{".sha1", ".sha256", ".sha512", ".md5", ".asc", ".sig", ".sbom"}

// primaryFile picks the file a user wants from one version. With a pattern (a
// glob on the file name, case-insensitive) it is the largest match; with none,
// or when nothing matches, the largest file that is not a checksum or
// signature. sizes may be empty, then the first (sorted) file wins.
func primaryFile(paths []string, sizes map[string]int64, pattern string) string {
	var cands []string
	for _, p := range paths {
		skip := false
		for _, suf := range companionSuffix {
			skip = skip || strings.HasSuffix(p, suf)
		}
		if !skip {
			cands = append(cands, p)
		}
	}
	largest := func(list []string) string {
		best := ""
		for _, p := range list {
			if best == "" || sizes[p] > sizes[best] || (sizes[p] == sizes[best] && p < best) {
				best = p
			}
		}
		return best
	}
	if pattern != "" {
		var matched []string
		pat := strings.ToLower(pattern)
		for _, p := range cands {
			if ok, _ := path.Match(pat, strings.ToLower(path.Base(p))); ok {
				matched = append(matched, p)
			}
		}
		if len(matched) > 0 {
			return largest(matched)
		}
	}
	if best := largest(cands); best != "" {
		return best
	}
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

// unitTier ranks what kind of version a unit is: stable releases first, then
// pre-releases, then anything that is not a version at all (plain file names).
func unitTier(version string) int {
	switch {
	case !registry.IsVersionLike(version):
		return 0
	case registry.IsPreRelease(version):
		return 1
	}
	return 2
}

// newerUnit reports whether a should be listed before b.
func newerUnit(a, b registry.ArtifactUnit) bool {
	ta, tb := unitTier(a.Version), unitTier(b.Version)
	if ta != tb {
		return ta > tb
	}
	if ta > 0 {
		if c := registry.CompareVersions(a.Version, b.Version); c != 0 {
			return c > 0
		}
	}
	if a.CreatedAt != b.CreatedAt {
		return a.CreatedAt > b.CreatedAt
	}
	return a.Version > b.Version
}

// buildPackages folds stored objects into packages with their versions, newest
// first. The latest version is the highest stable one; upload time breaks ties
// and orders anything that is not a version (a plain file name).
func buildPackages(format registry.Format, infos []storage.ObjectInfo, primaryPattern string) []packageView {
	sizes := map[string]int64{}
	for _, o := range infos {
		sizes[o.Path] = o.Size
	}
	names, byPkg := groupPackages(format, infos)
	out := make([]packageView, 0, len(names))
	for _, n := range names {
		us := byPkg[n]
		pv := packageView{Name: n, Latest: versionOf(us[0], sizes, primaryPattern), Unversioned: unitTier(us[0].Version) == 0}
		for _, u := range us[1:] {
			if len(pv.Others) >= otherVersions {
				break
			}
			pv.Others = append(pv.Others, versionOf(u, sizes, primaryPattern))
		}
		out = append(out, pv)
	}
	// The first package is the one the page offers as "the" download, so the
	// order matters: packages whose latest files match the main-file pattern
	// first, then real versions before plain files with no version in the name
	// (a manifest.json next to an extension), then by name.
	matched := func(p packageView) bool {
		return primaryPattern != "" && matchesPrimary(p.Latest.Files, primaryPattern)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if ma, mb := matched(a), matched(b); ma != mb {
			return ma
		}
		if ta, tb := unitTier(a.Latest.Version), unitTier(b.Latest.Version); ta != tb {
			return ta > tb
		}
		return a.Name < b.Name
	})
	if len(out) > maxPackages {
		out = out[:maxPackages]
	}
	return out
}

// matchesPrimary reports whether any (non-checksum) file matches the pattern.
func matchesPrimary(files []fileView, pattern string) bool {
	pat := strings.ToLower(pattern)
	for _, f := range files {
		skip := false
		for _, suf := range companionSuffix {
			skip = skip || strings.HasSuffix(f.Path, suf)
		}
		if skip {
			continue
		}
		if ok, _ := path.Match(pat, strings.ToLower(path.Base(f.Path))); ok {
			return true
		}
	}
	return false
}

// groupPackages folds stored objects into packages, each with its versions
// newest first (see newerUnit). Package names come back sorted.
func groupPackages(format registry.Format, infos []storage.ObjectInfo) ([]string, map[string][]registry.ArtifactUnit) {
	byPkg := map[string][]registry.ArtifactUnit{}
	var names []string
	for _, u := range registry.GroupArtifacts(format, infos) {
		if _, ok := byPkg[u.Package]; !ok {
			names = append(names, u.Package)
		}
		byPkg[u.Package] = append(byPkg[u.Package], u)
	}
	sort.Strings(names)
	for _, n := range names {
		us := byPkg[n]
		sort.SliceStable(us, func(i, j int) bool { return newerUnit(us[i], us[j]) })
	}
	return names, byPkg
}

func versionOf(u registry.ArtifactUnit, sizes map[string]int64, primaryPattern string) versionView {
	paths := append([]string(nil), u.Paths...)
	sort.Strings(paths)
	fv := make([]fileView, len(paths))
	for i, p := range paths {
		fv[i] = fileView{Path: p, Size: sizes[p]}
	}
	return versionView{Version: u.Version, Created: u.CreatedAt / 1e6, Primary: primaryFile(paths, sizes, primaryPattern), Files: fv}
}

// pickLatestTag chooses the tag to pull for the newest version of an image.
func pickLatestTag(tags []tagView) string {
	if len(tags) == 0 {
		return ""
	}
	best, bestStable := "", ""
	hasLatest := false
	for _, t := range tags {
		if t.Tag == "latest" {
			hasLatest = true
		}
		if !registry.IsVersionLike(t.Tag) {
			continue
		}
		if best == "" || registry.CompareVersions(t.Tag, best) > 0 {
			best = t.Tag
		}
		if !registry.IsPreRelease(t.Tag) && (bestStable == "" || registry.CompareVersions(t.Tag, bestStable) > 0) {
			bestStable = t.Tag
		}
	}
	switch {
	case bestStable != "":
		return bestStable
	case hasLatest:
		return "latest"
	case best != "":
		return best
	}
	newest := tags[0]
	for _, t := range tags[1:] {
		if t.PushedAt > newest.PushedAt {
			newest = t
		}
	}
	return newest.Tag
}

// ---- screenshots ----

// catalogVisible reports whether the caller may see the app: at least one of
// its artifacts is readable. It is the one rule behind the list, the page and
// the screenshots.
func (h *Handler) catalogVisible(c *gin.Context, a *storage.CatalogApp) bool {
	for _, ar := range a.Artifacts {
		if _, ok := h.artifactVisible(c, ar); ok {
			return true
		}
	}
	return false
}

// catalogScreenshot serves GET /api/v1/catalog/:slug/screenshots/:id. It sits
// behind the same visibility rule as the page, so a hidden app leaks no image.
func (h *Handler) catalogScreenshot(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	sid, ok := pathID(c, "id")
	if !ok {
		return
	}
	a, err := h.catalog.GetCatalogApp(c.Param("slug"))
	if err != nil || !h.catalogVisible(c, a) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	img, err := h.catalog.CatalogScreenshotImage(a.ID, sid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	m := iconDataRe.FindStringSubmatch(img)
	if m == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	c.Header("Cache-Control", "private, max-age=300")
	c.Data(http.StatusOK, "image/"+m[1], raw)
}

// validateScreenshot checks an uploaded image data URI.
func validateScreenshot(image, caption string) error {
	m := iconDataRe.FindStringSubmatch(image)
	if m == nil {
		return errors.New("screenshot must be a png, jpeg, webp or gif image")
	}
	raw, err := base64.StdEncoding.DecodeString(m[2])
	if err != nil || len(raw) == 0 {
		return errors.New("screenshot image is invalid")
	}
	if len(raw) > maxShotBytes {
		return errors.New("screenshot is larger than 512 KB")
	}
	if len(caption) > 200 {
		return errors.New("caption must be at most 200 characters")
	}
	return nil
}

func (h *Handler) adminAddScreenshot(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var b struct {
		Image   string `json:"image"`
		Caption string `json:"caption"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := validateScreenshot(b.Image, b.Caption); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.catalog.GetCatalogAppByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	if len(a.Screenshots) >= maxShots {
		c.JSON(http.StatusBadRequest, gin.H{"error": "at most 8 screenshots per application"})
		return
	}
	sid, err := h.catalog.AddCatalogScreenshot(id, b.Image, b.Caption)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"id": sid})
}

func (h *Handler) adminUpdateScreenshot(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	sid, ok := pathID(c, "sid")
	if !ok {
		return
	}
	var b struct {
		Caption string `json:"caption"`
	}
	if err := c.ShouldBindJSON(&b); err != nil || len(b.Caption) > 200 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "caption must be at most 200 characters"})
		return
	}
	if err := h.catalog.UpdateCatalogScreenshot(id, sid, b.Caption); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "screenshot not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) adminDeleteScreenshot(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	sid, ok := pathID(c, "sid")
	if !ok {
		return
	}
	if err := h.catalog.DeleteCatalogScreenshot(id, sid); errors.Is(err, storage.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "screenshot not found"})
		return
	} else if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}

// sameIDs reports whether a and b hold the same ids, each once, in any order.
func sameIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[int64]int{}
	for _, x := range a {
		seen[x]++
	}
	for _, x := range b {
		seen[x]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func (h *Handler) adminReorderScreenshots(c *gin.Context) {
	if !h.catalogReady(c) {
		return
	}
	id, ok := pathID(c, "id")
	if !ok {
		return
	}
	var b struct {
		IDs []int64 `json:"ids"`
	}
	if err := c.ShouldBindJSON(&b); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	a, err := h.catalog.GetCatalogAppByID(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "application not found"})
		return
	}
	current := make([]int64, len(a.Screenshots))
	for i, s := range a.Screenshots {
		current[i] = s.ID
	}
	if !sameIDs(current, b.IDs) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "the order must list every screenshot of the application exactly once"})
		return
	}
	if err := h.catalog.ReorderCatalogScreenshots(id, b.IDs); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusNoContent)
}
