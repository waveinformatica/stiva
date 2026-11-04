package api

import (
	"encoding/base64"
	"strings"
	"testing"

	"registry/internal/registry"
	"registry/internal/storage"
)

// knownRegistries stands in for the registry lookup: "docker" is an OCI
// registry, every other known name a raw one.
func knownRegistries(names ...string) func(string) (string, bool) {
	return func(n string) (string, bool) {
		for _, k := range names {
			if k == n {
				if n == "docker" {
					return "oci", true
				}
				return "raw", true
			}
		}
		return "", false
	}
}

func goodApp() storage.CatalogApp {
	return storage.CatalogApp{
		Slug: "acme-installer", Name: "Acme Installer", Tagline: "Install Acme",
		Artifacts: []storage.CatalogArtifact{{Registry: "docker", Ref: "acme/app"}},
	}
}

func TestVisibleArtifactsHideTheRestAndTheApp(t *testing.T) {
	app := storage.CatalogApp{Name: "X", Artifacts: []storage.CatalogArtifact{
		{Registry: "public", Ref: "acme/app"},
		{Registry: "private", Ref: "acme/internal"},
		{Registry: "public", Ref: "acme/cli"},
	}}
	readable := func(ar storage.CatalogArtifact) bool { return ar.Registry == "public" }
	got := visibleArtifacts(app, readable)
	if len(got) != 2 || got[0].Ref != "acme/app" || got[1].Ref != "acme/cli" {
		t.Errorf("visible = %+v, want the two public artifacts in order", got)
	}
	// A caller who can read none of them gets nothing, so the app is not listed.
	if got := visibleArtifacts(app, func(storage.CatalogArtifact) bool { return false }); len(got) != 0 {
		t.Errorf("visible = %+v, want none", got)
	}
}

func TestValidateCatalogApp(t *testing.T) {
	exists := knownRegistries("docker")
	a := goodApp()
	if err := validateCatalogApp(&a, exists); err != nil {
		t.Fatalf("valid app rejected: %v", err)
	}

	png := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte("tiny"))
	big := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", maxIconBytes+1)))
	for name, mutate := range map[string]func(*storage.CatalogApp){
		"bad slug":         func(a *storage.CatalogApp) { a.Slug = "Not A Slug" },
		"slug with slash":  func(a *storage.CatalogApp) { a.Slug = "a/b" },
		"no name":          func(a *storage.CatalogApp) { a.Name = " " },
		"long tagline":     func(a *storage.CatalogApp) { a.Tagline = strings.Repeat("x", 201) },
		"bad homepage":     func(a *storage.CatalogApp) { a.Homepage = "javascript:alert(1)" },
		"svg icon":         func(a *storage.CatalogApp) { a.Icon = "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=" },
		"http icon":        func(a *storage.CatalogApp) { a.Icon = "http://x/y.png" },
		"oversized icon":   func(a *storage.CatalogApp) { a.Icon = big },
		"unknown registry": func(a *storage.CatalogApp) { a.Artifacts[0].Registry = "nope" },
		"empty ref":        func(a *storage.CatalogApp) { a.Artifacts[0].Ref = " / " },
	} {
		a := goodApp()
		a.Artifacts = append([]storage.CatalogArtifact(nil), a.Artifacts...)
		mutate(&a)
		if err := validateCatalogApp(&a, exists); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for name, icon := range map[string]string{"png data": png, "https url": "https://cdn.example.com/a.png", "none": ""} {
		a := goodApp()
		a.Icon = icon
		if err := validateCatalogApp(&a, exists); err != nil {
			t.Errorf("%s icon rejected: %v", name, err)
		}
	}
	// Surrounding slashes in a path are normalised away.
	a2 := goodApp()
	a2.Artifacts[0].Ref = "/acme/app/"
	_ = validateCatalogApp(&a2, exists)
	if a2.Artifacts[0].Ref != "acme/app" {
		t.Errorf("ref = %q, want acme/app", a2.Artifacts[0].Ref)
	}
}

func TestBuildPackagesPicksTheHighestStableVersion(t *testing.T) {
	const day = int64(24 * 3600 * 1e9)
	infos := []storage.ObjectInfo{
		{Path: "dl/acme-1.9.0.tar.gz", Size: 900, CreatedAt: 1 * day},
		{Path: "dl/acme-1.10.0.tar.gz", Size: 1000, CreatedAt: 2 * day},
		{Path: "dl/acme-1.10.0.tar.gz.sha256", Size: 64, CreatedAt: 2 * day},
		// Re-uploaded later, but older by version: must not become "latest".
		{Path: "dl/acme-1.2.0.tar.gz", Size: 800, CreatedAt: 9 * day},
		// Newer by version, but a pre-release: stable 1.10.0 still wins.
		{Path: "dl/acme-2.0.0-rc1.tar.gz", Size: 1100, CreatedAt: 8 * day},
	}
	pk := buildPackages(registry.FormatRaw, infos, "")
	if len(pk) != 1 {
		t.Fatalf("packages = %d, want 1", len(pk))
	}
	p := pk[0]
	if p.Latest.Version != "1.10.0" {
		t.Errorf("latest = %s, want 1.10.0 (highest stable, not the newest upload, not the rc)", p.Latest.Version)
	}
	if p.Latest.Primary != "dl/acme-1.10.0.tar.gz" {
		t.Errorf("primary = %s, want the archive, not its checksum", p.Latest.Primary)
	}
	if len(p.Latest.Files) != 2 {
		t.Errorf("latest lists %d files, want the archive and its checksum", len(p.Latest.Files))
	}
	var order []string
	for _, o := range p.Others {
		order = append(order, o.Version)
	}
	// Stable versions descending, then the pre-release.
	if got := strings.Join(order, ","); got != "1.9.0,1.2.0,2.0.0-rc1" {
		t.Errorf("others = %s, want 1.9.0,1.2.0,2.0.0-rc1", got)
	}
}

func TestBuildPackagesPlainFilesFallBackToUploadTime(t *testing.T) {
	infos := []storage.ObjectInfo{
		{Path: "tools/old.exe", Size: 10, CreatedAt: 1},
		{Path: "tools/new.exe", Size: 10, CreatedAt: 5},
	}
	pk := buildPackages(registry.FormatRaw, infos, "")
	if len(pk) != 1 || pk[0].Latest.Primary != "tools/new.exe" {
		t.Errorf("latest = %+v, want the most recent upload when names carry no version", pk)
	}
}

func TestPickLatestTag(t *testing.T) {
	tag := func(names ...string) []tagView {
		var out []tagView
		for i, n := range names {
			out = append(out, tagView{Tag: n, PushedAt: int64(i)})
		}
		return out
	}
	cases := []struct {
		name string
		tags []tagView
		want string
	}{
		{"highest stable semver", tag("1.9.0", "1.10.0", "latest", "2.0.0-rc1"), "1.10.0"},
		{"only prerelease and latest", tag("2.0.0-rc1", "latest"), "latest"},
		{"no versions, latest exists", tag("main", "latest"), "latest"},
		{"nothing version-like", tag("main", "dev"), "dev"}, // dev was pushed last
		{"empty", nil, ""},
	}
	for _, c := range cases {
		if got := pickLatestTag(c.tags); got != c.want {
			t.Errorf("%s: pickLatestTag = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScreenshotValidationAndOrder(t *testing.T) {
	ok := "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString([]byte("jpegbytes"))
	if err := validateScreenshot(ok, "Dashboard"); err != nil {
		t.Errorf("valid screenshot rejected: %v", err)
	}
	big := "data:image/png;base64," + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", maxShotBytes+1)))
	for name, img := range map[string]string{
		"svg": "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=", "oversized": big, "not a data uri": "https://x/y.png", "empty": "",
	} {
		if err := validateScreenshot(img, ""); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if err := validateScreenshot(ok, strings.Repeat("c", 201)); err == nil {
		t.Error("long caption accepted")
	}
	if !sameIDs([]int64{1, 2, 3}, []int64{3, 1, 2}) {
		t.Error("a permutation must be accepted")
	}
	for _, bad := range [][]int64{{1, 2}, {1, 2, 2}, {1, 2, 4}, {1, 2, 3, 4}} {
		if sameIDs([]int64{1, 2, 3}, bad) {
			t.Errorf("%v accepted as an order of 1,2,3", bad)
		}
	}
}

func TestPrimaryFilePatternPicksTheMainDownload(t *testing.T) {
	paths := []string{
		"dl/acme-1.0-linux-amd64.tar.gz", "dl/acme-1.0-windows-amd64.zip", "dl/acme-1.0-src.tar.gz",
		"dl/acme-1.0-linux-amd64.tar.gz.sha256",
	}
	sizes := map[string]int64{paths[0]: 50, paths[1]: 60, paths[2]: 900, paths[3]: 1}
	// No pattern: the largest real file, here the source archive.
	if got := primaryFile(paths, sizes, ""); got != "dl/acme-1.0-src.tar.gz" {
		t.Errorf("default = %s", got)
	}
	// The pattern overrides size, and ignores case and directory.
	if got := primaryFile(paths, sizes, "*-LINUX-amd64.tar.gz"); got != "dl/acme-1.0-linux-amd64.tar.gz" {
		t.Errorf("linux pattern = %s", got)
	}
	// A pattern that matches nothing falls back instead of leaving no download.
	if got := primaryFile(paths, sizes, "*.dmg"); got != "dl/acme-1.0-src.tar.gz" {
		t.Errorf("unmatched pattern = %s, want the default pick", got)
	}
	// The checksum is never the answer, even if it is all that matches.
	if got := primaryFile(paths, sizes, "*.sha256"); got == paths[3] {
		t.Error("a checksum was chosen as the main file")
	}
	a := goodApp()
	a.Artifacts[0].Primary = "["
	if err := validateCatalogApp(&a, knownRegistries("docker")); err == nil {
		t.Error("an invalid glob was accepted")
	}
}

func TestFolderPrefixKeepsItsTrailingSlash(t *testing.T) {
	look := knownRegistries("docker", "files")
	for _, c := range []struct{ registry, in, want string }{
		{"files", "builds/", "builds/"}, // a folder stays a folder: it must not match builds-old/
		{"files", "/builds//", "builds/"},
		{"files", "builds/app-1.0.tar.gz", "builds/app-1.0.tar.gz"},
		{"docker", "/acme/app/", "acme/app"}, // an image repository never has one
	} {
		a := goodApp()
		a.Artifacts = []storage.CatalogArtifact{{Registry: c.registry, Ref: c.in}}
		if err := validateCatalogApp(&a, look); err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if a.Artifacts[0].Ref != c.want {
			t.Errorf("%s %q -> %q, want %q", c.registry, c.in, a.Artifacts[0].Ref, c.want)
		}
	}
	a := goodApp()
	a.Artifacts = []storage.CatalogArtifact{{Registry: "files", Ref: " / "}}
	if err := validateCatalogApp(&a, look); err == nil {
		t.Error("an empty path was accepted")
	}
}
