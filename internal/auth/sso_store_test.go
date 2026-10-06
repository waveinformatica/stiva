package auth

import (
	"strings"
	"testing"

	"registry/internal/storage"
)

func TestValidSSOID(t *testing.T) {
	for _, ok := range []string{"m365", "my-provider-2", "x"} {
		if !validSSOID(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "UPPER", "with space", "under_score", "dot.com", "slash/x", strings.Repeat("a", 65)} {
		if validSSOID(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestResolveSSOInput(t *testing.T) {
	// Create with secret: resolves and reports the effective secret.
	rp, secret, err := resolveSSOInput(SSOConfig{
		ID: "Gh", Provider: "github", ClientID: "c", Secret: "s",
	}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rp.Kind != SSOKindOAuth2 || secret != "s" {
		t.Fatalf("create = %+v %q", rp, secret)
	}
	// Update without secret keeps the stored one.
	rp, secret, err = resolveSSOInput(SSOConfig{
		ID: "gh", Provider: "github", ClientID: "c",
	}, "kept")
	if err != nil || secret != "kept" {
		t.Fatalf("keep = %+v %q %v", rp, secret, err)
	}
	// Update without any secret anywhere fails for presets that need one.
	if _, _, err := resolveSSOInput(SSOConfig{
		ID: "gh", Provider: "github", ClientID: "c",
	}, ""); err == nil {
		t.Fatal("missing secret should fail")
	}
	// ... but not for presets that need none.
	if _, secret, err := resolveSSOInput(SSOConfig{
		ID: "gl", Provider: "gitlab", ClientID: "c",
	}, ""); err != nil || secret != "" {
		t.Fatalf("secretless gitlab = %q %v", secret, err)
	}
	// Bad ids and unknown providers fail before any I/O.
	for _, in := range []SSOConfig{
		{ID: "BAD ID", Provider: "github", ClientID: "c", Secret: "s"},
		{ID: "", Provider: "github", ClientID: "c", Secret: "s"},
		{ID: "x", Provider: "saml", ClientID: "c"},
		{ID: "m", Provider: "microsoft", ClientID: "c", Secret: "s"},
	} {
		if _, _, err := resolveSSOInput(in, ""); err == nil {
			t.Fatalf("%+v should fail", in)
		}
	}
}

func TestSSORecordRoundTrip(t *testing.T) {
	in := SSOConfig{
		ID: "m365", Label: "M", Provider: "microsoft", Enabled: true,
		ClientID: "c", Tenant: "t", Scope: "openid", Username: "upn",
		Groups: "groups", Audience: "a", AdminGroup: "g",
	}
	rec := ssoInputToRecord(in)
	if rec.ID != "m365" || !rec.Enabled || rec.Scope != "openid" {
		t.Fatalf("record = %+v", rec)
	}
	back := ssoRecordToInput(rec)
	back.Secret = ""
	in.Secret = ""
	if back != in {
		t.Fatalf("round trip:\n%+v\n%+v", back, in)
	}
	v := ssoViewOf(rec)
	if v.Kind != SSOKindOIDC || v.HasSecret || v.Label != "M" {
		t.Fatalf("view = %+v", v)
	}
	var _ storage.SSOProviderRecord = rec
}

func TestPresetRequiresSecret(t *testing.T) {
	for _, k := range []string{"microsoft", "github", "linkedin"} {
		if !presetRequiresSecret(k) {
			t.Errorf("%s should require a secret", k)
		}
	}
	for _, k := range []string{"google", "gitlab", "oidc", "oauth2", "cas", "nope"} {
		if presetRequiresSecret(k) {
			t.Errorf("%s should not require a secret", k)
		}
	}
}

func TestSSOPresetsShape(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range SSOPresets() {
		if p.Key == "" || p.Label == "" || p.Kind == "" {
			t.Fatalf("preset = %+v", p)
		}
		if seen[p.Key] {
			t.Fatalf("duplicate preset %q", p.Key)
		}
		seen[p.Key] = true
		inFields := map[string]bool{}
		for _, f := range p.Fields {
			inFields[f] = true
		}
		for _, r := range p.Required {
			key := r
			switch r {
			case "secret":
				key = "secret"
			case "authorize":
				key = "authorize_url"
			case "token":
				key = "token_url"
			case "userinfo":
				key = "userinfo_url"
			}
			if !inFields[key] {
				t.Fatalf("preset %q requires %q not in its fields %v", p.Key, r, p.Fields)
			}
		}
	}
	if len(seen) != 8 {
		t.Fatalf("presets = %d, want 8", len(seen))
	}
}
