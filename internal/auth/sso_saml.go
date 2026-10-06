package auth

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net/url"
	"strings"

	saml2 "github.com/russellhaering/gosaml2"
	dsig "github.com/russellhaering/goxmldsig"
)

// SAML 2.0 login works like this: the browser is redirected to the IdP with
// a signed-by-nothing AuthnRequest (unsigned requests are accepted by default
// on Keycloak, Entra ID, Google and Okta; an IdP configured to mandate signed
// requests refuses them with its own error page), and the IdP POSTs a signed
// response to our callback. Responses must always be signed — there is no
// switch to turn that off — and carry audience, recipient and time conditions
// the library enforces. Encrypted assertions are not supported: IdPs sign by
// default, encrypt only when asked, so leave encryption off at the IdP.

// parseSAMLIdPCert parses the configured IdP signing certificate (PEM). The
// first CERTIFICATE block wins; extras, if an admin pastes a chain, are
// ignored rather than rejected.
func parseSAMLIdPCert(pemData string) (*x509.Certificate, error) {
	rest := []byte(strings.TrimSpace(pemData))
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return nil, fmt.Errorf("auth: sso: no certificate found in idp_cert")
		}
		if block.Type == "CERTIFICATE" {
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				return nil, fmt.Errorf("auth: sso: invalid idp_cert: %w", err)
			}
			return cert, nil
		}
		if len(rest) == 0 {
			return nil, fmt.Errorf("auth: sso: no certificate found in idp_cert")
		}
	}
}

// samlSP builds the service provider view for one request. The audience is
// the stable entity ID from configuration; the assertion consumer URL and
// recipient come from the incoming request, so the same provider works
// behind any ingress without extra configuration.
func (p *ssoProvider) samlSP(callback string) (*saml2.SAMLServiceProvider, error) {
	cert, err := parseSAMLIdPCert(p.cfg.IdPCert)
	if err != nil {
		return nil, err
	}
	return &saml2.SAMLServiceProvider{
		IdentityProviderSSOURL:      p.cfg.IdPSSOURL,
		AssertionConsumerServiceURL: callback,
		ServiceProviderIssuer:       p.cfg.EntityID,
		AudienceURI:                 p.cfg.EntityID,
		IDPCertificateStore: &dsig.MemoryX509CertificateStore{
			Roots: []*x509.Certificate{cert},
		},
	}, nil
}

// validateSAMLProvider checks the SAML-specific configuration: a parseable
// https URL for the IdP, a parseable certificate, and a non-empty entity ID.
func validateSAMLProvider(r *resolvedProvider) error {
	u, err := url.ParseRequestURI(r.IdPSSOURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("auth: sso %q: invalid idp_sso_url", r.ID)
	}
	if _, err := parseSAMLIdPCert(r.IdPCert); err != nil {
		return fmt.Errorf("auth: sso %q: %v", r.ID, err)
	}
	if strings.TrimSpace(r.EntityID) == "" {
		return fmt.Errorf("auth: sso %q: entity_id is required (it must match the IdP registration)", r.ID)
	}
	return nil
}

// loginSAMLURL builds the IdP redirect carrying the AuthnRequest, with our
// state token riding along as RelayState for CSRF binding.
func (p *ssoProvider) loginSAMLURL(callback, state string) (string, error) {
	sp, err := p.samlSP(callback)
	if err != nil {
		return "", err
	}
	doc, err := sp.BuildAuthRequestDocumentNoSig()
	if err != nil {
		return "", fmt.Errorf("auth: sso %q: cannot build authn request: %w", p.cfg.ID, err)
	}
	target, err := sp.BuildAuthURLRedirect(state, doc)
	if err != nil {
		return "", fmt.Errorf("auth: sso %q: cannot build login url: %w", p.cfg.ID, err)
	}
	return target, nil
}

// validateSAMLResponse checks a base64 response from the IdP (POST or query
// parameter alike) and maps the assertion to a registry user: NameID unless a
// username attribute is configured, groups from the groups attribute.
func (p *ssoProvider) validateSAMLResponse(_ context.Context, callback, encoded string) (*User, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, fmt.Errorf("auth: sso %q: missing SAML response", p.cfg.ID)
	}
	sp, err := p.samlSP(callback)
	if err != nil {
		return nil, err
	}
	ai, err := sp.RetrieveAssertionInfo(encoded)
	if err != nil {
		return nil, fmt.Errorf("auth: sso %q: invalid assertion: %w", p.cfg.ID, err)
	}
	// gosaml2 reports audience mismatch and expired conditions as warnings,
	// not errors. For a login these are rejections: an assertion meant for
	// someone else, or outside its validity window, must never authenticate.
	if ai.WarningInfo != nil && (ai.WarningInfo.NotInAudience || ai.WarningInfo.InvalidTime) {
		return nil, fmt.Errorf("auth: sso %q: assertion not for this service or outside its validity window", p.cfg.ID)
	}
	name := strings.TrimSpace(ai.NameID)
	if p.cfg.Username != "" {
		if v := samlAttrFirst(ai, p.cfg.Username); v != "" {
			name = v
		}
	}
	u := &User{Name: name, Groups: samlAttrAll(ai, p.cfg.Groups), Source: p.Name()}
	p.applyAdmin(u)
	return u, nil
}

// samlAttrAll returns every value of a SAML attribute, in document order.
func samlAttrAll(ai *saml2.AssertionInfo, name string) []string {
	if ai == nil || name == "" {
		return nil
	}
	attr, ok := ai.Values[name]
	if !ok {
		return nil
	}
	var out []string
	for _, v := range attr.Values {
		if s := strings.TrimSpace(v.Value); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// samlAttrFirst returns the first value of a SAML attribute, or "".
func samlAttrFirst(ai *saml2.AssertionInfo, name string) string {
	if v := samlAttrAll(ai, name); len(v) > 0 {
		return v[0]
	}
	return ""
}
