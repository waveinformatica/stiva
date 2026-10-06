package api

import "testing"

// TestParseRepoSub covers repository names containing slashes. A :name route
// parameter matches a single segment and %2F is decoded before routing, so
// namespaced repositories used to fall through to the SPA catch-all and the UI
// got HTML where it expected JSON.
func TestParseRepoSub(t *testing.T) {
	cases := []struct{ in, kind, repo, ref string }{
		{"/", "list", "", ""},
		{"", "list", "", ""},
		{"/builder/tags", "tags", "builder", ""},
		{"/apeiron/iot-hub/tags", "tags", "apeiron/iot-hub", ""},
		{"/fca-nbes/fca-nbes/upm/tags", "tags", "fca-nbes/fca-nbes/upm", ""},
		{"/builder/manifests/latest", "manifest", "builder", "latest"},
		{"/apeiron/iot-hub/manifests/latest", "manifest", "apeiron/iot-hub", "latest"},
		{"/kosmos/core/manifests/sha256:abc", "manifest", "kosmos/core", "sha256:abc"},
		// A repository literally named "…/manifests" must not swallow the ref.
		{"/a/manifests/manifests/v1", "manifest", "a/manifests", "v1"},
		{"/tags", "", "", ""},
		{"/repo/manifests/", "", "", ""},
	}
	for _, c := range cases {
		k, r, f := parseRepoSub(c.in)
		if k != c.kind || r != c.repo || f != c.ref {
			t.Errorf("parseRepoSub(%q) = (%q,%q,%q), atteso (%q,%q,%q)", c.in, k, r, f, c.kind, c.repo, c.ref)
		}
	}
}
