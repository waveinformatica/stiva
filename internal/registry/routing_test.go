package registry

import (
	"strings"
	"testing"
)

func TestBasePathValidation(t *testing.T) {
	valid := []string{"", "maven-central", "npm", "a/b/c", "/maven-central/", "my_repo-1.0"}
	for _, b := range valid {
		r := &Registry{Name: "x", Format: string(FormatMaven), Type: string(TypeHosted), BasePath: b}
		if err := r.Validate(); err != nil {
			t.Errorf("base %q should validate: %v", b, err)
		}
		if b != "" && r.BasePath != normalizeBasePath(b) {
			t.Errorf("base %q not normalized to %q", b, r.BasePath)
		}
	}
	invalid := []string{"../escape", "a/../b", "v2", "v2/foo", "api", "api/v1/x", "has space", "semi;colon", strings.Repeat("a", 129)}
	for _, b := range invalid {
		r := &Registry{Name: "x", Format: string(FormatMaven), Type: string(TypeHosted), BasePath: b}
		if err := r.Validate(); err == nil {
			t.Errorf("base %q should not validate", b)
		}
	}
}

func TestBasePathRejectedForOCI(t *testing.T) {
	for _, f := range []string{string(FormatDocker), "docker"} {
		r := &Registry{Name: "x", Format: f, Type: string(TypeHosted), BasePath: "oci-things"}
		if err := r.Validate(); err == nil {
			t.Errorf("format %q with a base path should not validate", f)
		}
	}
	r := &Registry{Name: "x", Format: string(FormatDocker), Type: string(TypeHosted)}
	if err := r.Validate(); err != nil {
		t.Errorf("OCI without base path should validate: %v", err)
	}
}

func TestBasePathMatch(t *testing.T) {
	cases := []struct {
		base, path string
		want       bool
	}{
		{"", "anything/at/all", true},
		{"", "", true},
		{"maven", "maven", true},
		{"maven", "maven/com/foo-1.0.jar", true},
		{"maven", "mavenx/com/foo.jar", false},
		{"maven", "other", false},
		{"maven", "", false},
		{"a/b", "a/b/c", true},
		{"a/b", "a/bc", false},
		{"a/b", "a", false},
	}
	for _, c := range cases {
		if got := basePathMatch(c.base, c.path); got != c.want {
			t.Errorf("basePathMatch(%q, %q) = %v, want %v", c.base, c.path, got, c.want)
		}
	}
}

func TestStripBasePath(t *testing.T) {
	cases := []struct{ base, path, want string }{
		{"", "a/b", "a/b"},
		{"maven", "maven", ""},
		{"maven", "maven/com/foo.jar", "com/foo.jar"},
		{"maven", "other", "other"},
		{"a/b", "a/b/c/d", "c/d"},
	}
	for _, c := range cases {
		if got := stripBasePath(c.base, c.path); got != c.want {
			t.Errorf("stripBasePath(%q, %q) = %q, want %q", c.base, c.path, got, c.want)
		}
	}
}

func routeManager() *Manager {
	m := &Manager{defs: make(map[string]*Registry)}
	put := func(r *Registry) {
		r.BasePath = normalizeBasePath(r.BasePath)
		m.defs[r.Name] = r
	}
	put(&Registry{Name: "oci-group", Format: "oci", Type: "group", Hosts: []string{"repo.example.com"}, Members: []string{"h", "p"}})
	put(&Registry{Name: "maven-central", Format: "maven", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "maven-central"})
	put(&Registry{Name: "maven-snap", Format: "maven", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "maven-central/snapshots"})
	put(&Registry{Name: "npmjs", Format: "npm", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "npm"})
	put(&Registry{Name: "legacy", Format: "raw", Type: "hosted"})
	put(&Registry{Name: "fallback", Format: "oci", Type: "hosted", Default: true})
	return m
}

func TestResolveSpecificity(t *testing.T) {
	m := routeManager()
	cases := []struct {
		host, path, wantReg, wantPath string
	}{
		// Empty OCI base coexists with pathed registries on the same host.
		{"repo.example.com", "v2/ubuntu/manifests/latest", "oci-group", "v2/ubuntu/manifests/latest"},
		// Longest prefix wins; nested prefixes nest.
		{"repo.example.com", "maven-central/com/foo-1.0.jar", "maven-central", "com/foo-1.0.jar"},
		{"repo.example.com", "maven-central/snapshots/com/foo.jar", "maven-snap", "com/foo.jar"},
		{"repo.example.com", "maven-central", "maven-central", ""},
		{"repo.example.com", "npm/lodash", "npmjs", "lodash"},
		// Segment boundary: no partial-segment theft.
		{"repo.example.com", "mavenx/com/foo.jar", "oci-group", "mavenx/com/foo.jar"},
		{"repo.example.com", "npmjs-fork/x", "oci-group", "npmjs-fork/x"},
		// Unknown host falls through to the single default.
		{"unknown.example.com", "v2/x/manifests/latest", "fallback", "v2/x/manifests/latest"},
		{"unknown.example.com", "maven-central/x", "fallback", "maven-central/x"},
	}
	for _, c := range cases {
		reg, rel, err := m.Resolve(c.host, "", c.path)
		if err != nil {
			t.Errorf("Resolve(%q, %q): %v", c.host, c.path, err)
			continue
		}
		if reg.Name != c.wantReg || rel != c.wantPath {
			t.Errorf("Resolve(%q, %q) = (%q, %q), want (%q, %q)",
				c.host, c.path, reg.Name, rel, c.wantReg, c.wantPath)
		}
	}
}

func TestResolvePinnedIgnoresBase(t *testing.T) {
	m := routeManager()
	m.defs["pinned-maven"] = &Registry{Name: "pinned-maven", Format: "maven", Type: "hosted", Port: 5001, BasePath: "maven-central"}
	reg, rel, err := m.Resolve("anything.example.com", "pinned-maven", "com/foo.jar")
	if err != nil {
		t.Fatalf("pinned resolve: %v", err)
	}
	if reg.Name != "pinned-maven" || rel != "com/foo.jar" {
		t.Fatalf("pinned resolve = (%q, %q), want native root passthrough", reg.Name, rel)
	}
}

func TestRouteConflicts(t *testing.T) {
	m := routeManager()
	dup := &Registry{Name: "clone", Format: "maven", Type: "hosted", Hosts: []string{"REPO.example.com"}, BasePath: "maven-central"}
	if err := m.checkRouteConflicts(dup); err == nil {
		t.Fatal("identical (host, base) on another registry should conflict")
	} else if !strings.Contains(err.Error(), "maven-central") {
		t.Fatalf("conflict error should name the route: %v", err)
	}
	overlap := &Registry{Name: "wider", Format: "maven", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "maven"}
	if err := m.checkRouteConflicts(overlap); err != nil {
		t.Fatalf("overlapping (not identical) prefixes must be allowed: %v", err)
	}
	otherBase := &Registry{Name: "other", Format: "maven", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "other"}
	if err := m.checkRouteConflicts(otherBase); err != nil {
		t.Fatalf("different base on the same host must be allowed: %v", err)
	}
	secondDefault := &Registry{Name: "d2", Format: "oci", Type: "hosted", Default: true}
	if err := m.checkRouteConflicts(secondDefault); err == nil {
		t.Fatal("a second default should conflict")
	}
	self := &Registry{Name: "maven-central", Format: "maven", Type: "hosted", Hosts: []string{"repo.example.com"}, BasePath: "maven-central"}
	if err := m.checkRouteConflicts(self); err != nil {
		t.Fatalf("updating a registry must not conflict with itself: %v", err)
	}
}
