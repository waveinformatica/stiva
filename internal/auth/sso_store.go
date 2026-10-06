package auth

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"registry/internal/storage"
	"registry/internal/vault"
)

// ErrSSOExists reports a duplicate provider id on create.
var ErrSSOExists = errors.New("auth: sso provider already exists")

// ErrSSONotFound reports an unknown provider id on update or delete.
var ErrSSONotFound = errors.New("auth: sso provider not found")

// SSOAdminView is the UI-facing shape of one provider: everything editable
// plus runtime facts. The client secret never appears here; HasSecret tells
// the form whether one is stored.
type SSOAdminView struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	Provider   string `json:"provider"`
	Kind       string `json:"kind"`
	ClientID   string `json:"client_id"`
	Tenant     string `json:"tenant"`
	BaseURL    string `json:"base_url"`
	Issuer     string `json:"issuer"`
	Authorize  string `json:"authorize_url"`
	Token      string `json:"token_url"`
	UserInfo   string `json:"userinfo_url"`
	Scope      string `json:"scope"`
	Username   string `json:"username_claim"`
	Groups     string `json:"groups_claim"`
	Audience   string `json:"audience"`
	AdminGroup string `json:"admin_group"`
	Enabled    bool   `json:"enabled"`
	HasSecret  bool   `json:"has_secret"`
	EntityID   string `json:"entity_id"`
	IdPSSOURL  string `json:"idp_sso_url"`
	IdPCert    string `json:"idp_cert"`
}

// ssoSecretKey scopes the vault entry holding a provider's client secret.
func ssoSecretKey(id string) string { return "sso/" + id }

// SetSSOBackend wires the persistence (database records) and the secret store
// (vault) behind UI-managed SSO providers. Until wired, ReloadSSO is a no-op
// and no provider is active.
func (m *Manager) SetSSOBackend(meta storage.MetadataStore, vlt *vault.Vault) {
	m.ssoMeta = meta
	m.ssoVault = vlt
}

// validSSOID reports whether s suits URLs and vault keys ([a-z0-9-]).
func validSSOID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' {
			return false
		}
	}
	return true
}

// resolveSSOInput normalizes and validates admin UI input against the stored
// secret (kept when the form leaves it empty), returning the resolved preset
// and the effective secret. Pure logic, no I/O: the UI cannot persist a
// provider that could never log anyone in.
func resolveSSOInput(in SSOConfig, storedSecret string) (*resolvedProvider, string, error) {
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	if !validSSOID(in.ID) {
		return nil, "", fmt.Errorf("auth: sso id %q is invalid (use [a-z0-9-])", in.ID)
	}
	secret := in.Secret
	if secret == "" {
		secret = storedSecret
	}
	tmp := in
	tmp.Secret = secret
	rp, err := resolveProvider(tmp)
	if err != nil {
		return nil, "", err
	}
	return rp, secret, nil
}

// presetRequiresSecret reports whether the preset mandates a client secret.
func presetRequiresSecret(provider string) bool {
	p, ok := providerPresets[strings.ToLower(strings.TrimSpace(provider))]
	if !ok {
		return false
	}
	for _, k := range p.require {
		if k == "secret" {
			return true
		}
	}
	return false
}

// ReloadSSO rebuilds the active providers from the database, resolving each
// client secret from the vault. Unreachable or misconfigured entries are
// skipped with a log line instead of breaking the others; disabled entries
// are ignored entirely.
func (m *Manager) ReloadSSO(ctx context.Context) error {
	if m.ssoMeta == nil {
		return nil
	}
	recs, err := m.ssoMeta.ListSSOProviders()
	if err != nil {
		return fmt.Errorf("auth: sso reload: %w", err)
	}
	var out []*ssoProvider
	for _, r := range recs {
		if !r.Enabled {
			continue
		}
		cfg := ssoRecordToInput(r)
		if presetRequiresSecret(r.Provider) {
			if m.ssoVault == nil {
				log.Printf("auth: sso %q skipped: no vault to read its client secret", r.ID)
				continue
			}
			sec, err := m.ssoVault.Resolve(ctx, vault.ScopeSSO, vault.NewRef(ssoSecretKey(r.ID)))
			if err != nil || sec == "" {
				log.Printf("auth: sso %q skipped: no client secret stored", r.ID)
				continue
			}
			cfg.Secret = sec
		}
		sp, err := NewSSOProvider(ctx, &cfg)
		if err != nil {
			log.Printf("auth: sso provider skipped: %v", err)
			continue
		}
		out = append(out, sp)
	}
	m.ssoMu.Lock()
	m.sso = out
	m.ssoMu.Unlock()
	return nil
}

// ListSSOProviders returns every stored provider for the admin UI.
func (m *Manager) ListSSOProviders(ctx context.Context) ([]SSOAdminView, error) {
	if m.ssoMeta == nil {
		return nil, fmt.Errorf("auth: sso storage not configured")
	}
	recs, err := m.ssoMeta.ListSSOProviders()
	if err != nil {
		return nil, err
	}
	out := make([]SSOAdminView, 0, len(recs))
	for _, r := range recs {
		v := ssoViewOf(r)
		if m.ssoVault != nil {
			if sec, err := m.ssoVault.Resolve(ctx, vault.ScopeSSO, vault.NewRef(ssoSecretKey(r.ID))); err == nil && sec != "" {
				v.HasSecret = true
			}
		}
		out = append(out, v)
	}
	return out, nil
}

// UpsertSSOProvider creates or replaces a provider from admin UI input. The
// Secret field carries a new client secret, or empty to keep the stored one.
// The definition is fully validated, so the UI cannot persist a provider that
// could never log anyone in.
func (m *Manager) UpsertSSOProvider(ctx context.Context, in SSOConfig, create bool) (SSOAdminView, error) {
	var empty SSOAdminView
	if m.ssoMeta == nil || m.ssoVault == nil {
		return empty, fmt.Errorf("auth: sso storage not configured")
	}
	in.ID = strings.ToLower(strings.TrimSpace(in.ID))
	recs, err := m.ssoMeta.ListSSOProviders()
	if err != nil {
		return empty, err
	}
	var existing *storage.SSOProviderRecord
	for i := range recs {
		if recs[i].ID == in.ID {
			existing = &recs[i]
			break
		}
	}
	if create && existing != nil {
		return empty, ErrSSOExists
	}
	if !create && existing == nil {
		return empty, ErrSSONotFound
	}
	var storedSecret string
	if existing != nil {
		storedSecret, _ = m.ssoVault.Resolve(ctx, vault.ScopeSSO, vault.NewRef(ssoSecretKey(in.ID)))
	}
	rp, secret, err := resolveSSOInput(in, storedSecret)
	if err != nil {
		return empty, err
	}
	if err := m.ssoMeta.UpsertSSOProvider(ssoInputToRecord(in)); err != nil {
		return empty, err
	}
	if in.Secret != "" {
		if _, err := m.ssoVault.Save(ctx, vault.ScopeSSO, ssoSecretKey(in.ID), in.Secret,
			"SSO client secret for "+in.ID, map[string]any{"provider": in.Provider}); err != nil {
			return empty, fmt.Errorf("auth: sso %q: cannot store client secret: %w (is REGISTRY_VAULT_KEY set?)", in.ID, err)
		}
	}
	if err := m.ReloadSSO(ctx); err != nil {
		return empty, err
	}
	v := ssoViewOf(ssoInputToRecord(in))
	v.Kind = rp.Kind
	v.HasSecret = secret != ""
	return v, nil
}

// DeleteSSOProvider drops a provider, its vault secret, and reloads.
func (m *Manager) DeleteSSOProvider(ctx context.Context, id string) error {
	if m.ssoMeta == nil || m.ssoVault == nil {
		return fmt.Errorf("auth: sso storage not configured")
	}
	recs, err := m.ssoMeta.ListSSOProviders()
	if err != nil {
		return err
	}
	found := false
	for _, r := range recs {
		if r.ID == id {
			found = true
			break
		}
	}
	if !found {
		return ErrSSONotFound
	}
	if err := m.ssoMeta.DeleteSSOProvider(id); err != nil {
		return err
	}
	if err := m.ssoVault.Delete(ctx, ssoSecretKey(id)); err != nil {
		log.Printf("auth: sso %q: vault cleanup: %v", id, err)
	}
	return m.ReloadSSO(ctx)
}

func ssoRecordToInput(r storage.SSOProviderRecord) SSOConfig {
	return SSOConfig{
		ID: r.ID, Label: r.Label, Provider: r.Provider, ClientID: r.ClientID,
		Tenant: r.Tenant, BaseURL: r.BaseURL, Issuer: r.Issuer, Authorize: r.AuthorizeURL,
		Token: r.TokenURL, UserInfo: r.UserinfoURL, Scope: r.Scope, Username: r.Username,
		Groups: r.Groups, Audience: r.Audience, AdminGroup: r.AdminGroup, Enabled: r.Enabled,
	}
}

func ssoInputToRecord(in SSOConfig) storage.SSOProviderRecord {
	return storage.SSOProviderRecord{
		ID: in.ID, Label: in.Label, Provider: in.Provider, ClientID: in.ClientID,
		Tenant: in.Tenant, BaseURL: in.BaseURL, Issuer: in.Issuer, AuthorizeURL: in.Authorize,
		TokenURL: in.Token, UserinfoURL: in.UserInfo, Scope: in.Scope, Username: in.Username,
		Groups: in.Groups, Audience: in.Audience, AdminGroup: in.AdminGroup, Enabled: in.Enabled,
		EntityID: in.EntityID, IdPSSOURL: in.IdPSSOURL, IdPCert: in.IdPCert,
	}
}

// ssoViewOf renders the stored record for the admin UI. Kind comes from the
// preset when known; the secret itself never appears.
func ssoViewOf(r storage.SSOProviderRecord) SSOAdminView {
	v := SSOAdminView{
		ID: r.ID, Label: r.Label, Provider: r.Provider, ClientID: r.ClientID,
		Tenant: r.Tenant, BaseURL: r.BaseURL, Issuer: r.Issuer, Authorize: r.AuthorizeURL,
		Token: r.TokenURL, UserInfo: r.UserinfoURL, Scope: r.Scope, Username: r.Username,
		Groups: r.Groups, Audience: r.Audience, AdminGroup: r.AdminGroup, Enabled: r.Enabled,
		EntityID: r.EntityID, IdPSSOURL: r.IdPSSOURL, IdPCert: r.IdPCert,
	}
	if p, ok := providerPresets[strings.ToLower(strings.TrimSpace(r.Provider))]; ok {
		v.Kind = p.kind
	}
	return v
}
