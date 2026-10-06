package auth

import (
	"strings"
	"testing"
)

func TestResolveKnownProviders(t *testing.T) {
	m365 := SSOConfig{ID: "m", Provider: "microsoft", ClientID: "cid", Secret: "sec", Tenant: "tenant.onmicrosoft.com"}
	r, err := resolveProvider(m365)
	if err != nil {
		t.Fatalf("microsoft: %v", err)
	}
	if r.Kind != SSOKindOIDC || r.Label != "Microsoft 365" {
		t.Fatalf("microsoft kind/label = %q/%q", r.Kind, r.Label)
	}
	for _, want := range []string{"tenant.onmicrosoft.com", "authorize", "token"} {
		if !strings.Contains(r.Authorize+r.Token+r.Issuer, want) {
			t.Fatalf("microsoft endpoints miss tenant: %+v", r)
		}
	}
	if r.Username != "preferred_username" || r.Scope == "" {
		t.Fatalf("microsoft defaults: %+v", r)
	}

	gh := SSOConfig{ID: "g", Provider: "github", ClientID: "cid", Secret: "sec"}
	r, err = resolveProvider(gh)
	if err != nil {
		t.Fatalf("github: %v", err)
	}
	if r.Kind != SSOKindOAuth2 || r.UserInfo != "https://api.github.com/user" || r.Username != "login" {
		t.Fatalf("github defaults: %+v", r)
	}

	gl := SSOConfig{ID: "gl", Provider: "gitlab", ClientID: "cid"}
	r, err = resolveProvider(gl)
	if err != nil {
		t.Fatalf("gitlab: %v", err)
	}
	if r.Kind != SSOKindOIDC || r.Issuer != "https://gitlab.com" {
		t.Fatalf("gitlab defaults: %+v", r)
	}
	glSelf := SSOConfig{ID: "gls", Provider: "gitlab", ClientID: "cid", BaseURL: "https://git.example.com/"}
	r, err = resolveProvider(glSelf)
	if err != nil || r.Issuer != "https://git.example.com" {
		t.Fatalf("gitlab self-hosted issuer: %+v %v", r, err)
	}

	goo := SSOConfig{ID: "go", Provider: "google", ClientID: "cid"}
	r, err = resolveProvider(goo)
	if err != nil || r.Kind != SSOKindOIDC || r.Issuer != "https://accounts.google.com" || r.Username != "email" {
		t.Fatalf("google defaults: %+v %v", r, err)
	}

	li := SSOConfig{ID: "li", Provider: "linkedin", ClientID: "cid", Secret: "sec"}
	r, err = resolveProvider(li)
	if err != nil || r.Kind != SSOKindOAuth2 || !strings.Contains(r.Token, "linkedin.com") {
		t.Fatalf("linkedin defaults: %+v %v", r, err)
	}

	cas := SSOConfig{ID: "c", Provider: "cas", BaseURL: "https://cas.example.com/"}
	r, err = resolveProvider(cas)
	if err != nil || r.Kind != SSOKindCAS || r.BaseURL != "https://cas.example.com" {
		t.Fatalf("cas defaults: %+v %v", r, err)
	}
}

func TestResolveProviderRequiresCredentials(t *testing.T) {
	cases := []SSOConfig{
		{ID: "m", Provider: "microsoft", ClientID: "c"},
		{ID: "g", Provider: "github", ClientID: "c"},
		{ID: "o", Provider: "oidc", ClientID: "c"},
		{ID: "o2", Provider: "oauth2", Authorize: "a", Token: "t"},
		{ID: "c", Provider: "cas"},
		{ID: "x", Provider: "saml"},
		{Provider: "github", ClientID: "c", Secret: "s"},
	}
	for i, c := range cases {
		if _, err := resolveProvider(c); err == nil {
			t.Errorf("case %d (%+v) should fail", i, c)
		}
	}
}

func TestResolveProviderOverrides(t *testing.T) {
	c := SSOConfig{
		ID: "g", Provider: "github", ClientID: "cid", Secret: "sec",
		Label: "Work GitHub", Scope: "read:user", Username: "email", Groups: "teams",
	}
	r, err := resolveProvider(c)
	if err != nil {
		t.Fatal(err)
	}
	if r.Label != "Work GitHub" || r.Scope != "read:user" || r.Username != "email" || r.Groups != "teams" {
		t.Fatalf("overrides lost: %+v", r)
	}
	if r.UserInfo != "https://api.github.com/user" {
		t.Fatalf("preset endpoint should survive overrides: %+v", r)
	}
}
