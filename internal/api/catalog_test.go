package api

import (
	"encoding/base64"
	"strings"
	"testing"

	"registry/internal/storage"
)

func knownRegistries(names ...string) func(string) bool {
	return func(n string) bool {
		for _, k := range names {
			if k == n {
				return true
			}
		}
		return false
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
