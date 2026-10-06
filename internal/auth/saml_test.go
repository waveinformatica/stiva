package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/beevik/etree"
	"github.com/gin-gonic/gin"
	dsig "github.com/russellhaering/goxmldsig"
)

type samlTestKey struct {
	key  *rsa.PrivateKey
	cert []byte
}

func (ks *samlTestKey) GetKeyPair() (*rsa.PrivateKey, []byte, error) {
	return ks.key, ks.cert, nil
}

func makeSAMLIdP(t *testing.T) (*samlTestKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemCert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	return &samlTestKey{key: key, cert: der}, pemCert
}

// signSAMLAssertion builds a minimal signed response for entity/audience,
// recipient and times around now, like a real IdP would emit.
func signSAMLAssertion(t *testing.T, ks *samlTestKey, entity, recipient, nameID string, attrs map[string][]string, notBefore, notAfter time.Time) string {
	t.Helper()
	ts := func(v time.Time) string { return v.UTC().Format("2006-01-02T15:04:05Z") }
	var ab strings.Builder
	ab.WriteString(`<saml:AttributeStatement>`)
	for name, vals := range attrs {
		ab.WriteString(`<saml:Attribute Name="` + name + `">`)
		for _, v := range vals {
			ab.WriteString(`<saml:AttributeValue>` + v + `</saml:AttributeValue>`)
		}
		ab.WriteString(`</saml:Attribute>`)
	}
	ab.WriteString(`</saml:AttributeStatement>`)
	doc := `<samlp:Response xmlns:samlp="urn:oasis:names:tc:SAML:2.0:protocol" xmlns:saml="urn:oasis:names:tc:SAML:2.0:assertion" ID="_resp1" Version="2.0" IssueInstant="` + ts(time.Now()) + `">` +
		`<saml:Issuer>https://idp.example.com</saml:Issuer>` +
		`<samlp:Status><samlp:StatusCode Value="urn:oasis:names:tc:SAML:2.0:status:Success"/></samlp:Status>` +
		`<saml:Assertion ID="_a1" Version="2.0" IssueInstant="` + ts(time.Now()) + `">` +
		`<saml:Issuer>https://idp.example.com</saml:Issuer>` +
		`<saml:Subject><saml:NameID>` + nameID + `</saml:NameID>` +
		`<saml:SubjectConfirmation Method="urn:oasis:names:tc:SAML:2.0:cm:bearer">` +
		`<saml:SubjectConfirmationData NotOnOrAfter="` + ts(notAfter) + `" Recipient="` + recipient + `"/>` +
		`</saml:SubjectConfirmation></saml:Subject>` +
		`<saml:Conditions NotBefore="` + ts(notBefore) + `" NotOnOrAfter="` + ts(notAfter) + `">` +
		`<saml:AudienceRestriction><saml:Audience>` + entity + `</saml:Audience></saml:AudienceRestriction>` +
		`</saml:Conditions>` + ab.String() +
		`</saml:Assertion></samlp:Response>`
	tree := etree.NewDocument()
	if err := tree.ReadFromBytes([]byte(doc)); err != nil {
		t.Fatal(err)
	}
	// Sign the response root the way gosaml2's own suite does: goxmldsig's
	// test-side helper mis-canonicalizes detached sub-elements, while real
	// IdPs emit correctly signed assertions. What this exercises is what
	// matters here — base64 transport, signature verification against the
	// IdP certificate, audience/recipient/time enforcement and mapping —
	// and the production path accepts signatures on either element.
	ctx := dsig.NewDefaultSigningContext(ks)
	signed, err := ctx.SignEnveloped(tree.Root())
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	out := etree.NewDocument()
	out.SetRoot(signed)
	raw, err := out.WriteToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

const samlTestEntity = "https://registry.example.com/saml-test"
const samlTestCallback = "http://registry.example.com/auth/sso/saml/callback"

func samlTestProvider(t *testing.T, pemCert string) *ssoProvider {
	t.Helper()
	p, err := NewSSOProvider(t.Context(), &SSOConfig{
		ID: "saml", Provider: "saml",
		EntityID: samlTestEntity, IdPSSOURL: "https://idp.example.com/sso", IdPCert: pemCert,
		Groups: "groups", AdminGroup: "admins",
	})
	if err != nil {
		t.Fatalf("saml provider: %v", err)
	}
	return p
}

func TestSAMLResolve(t *testing.T) {
	_, pemCert := makeSAMLIdP(t)
	r, err := resolveProvider(SSOConfig{
		ID: "s", Provider: "saml", EntityID: "https://sp.example.com",
		IdPSSOURL: "https://idp.example.com/sso", IdPCert: pemCert,
	})
	if err != nil {
		t.Fatalf("saml resolve: %v", err)
	}
	if r.Kind != SSOKindSAML || r.Label != "SAML 2.0" {
		t.Fatalf("kind/label = %q/%q", r.Kind, r.Label)
	}
	// Presence failures: each required field missing in turn.
	base := SSOConfig{
		ID: "s", Provider: "saml", EntityID: "https://sp.example.com",
		IdPSSOURL: "https://idp.example.com/sso", IdPCert: pemCert,
	}
	for _, drop := range []string{"entity_id", "idp_sso_url", "idp_cert"} {
		c := base
		switch drop {
		case "entity_id":
			c.EntityID = ""
		case "idp_sso_url":
			c.IdPSSOURL = ""
		case "idp_cert":
			c.IdPCert = ""
		}
		if _, err := resolveProvider(c); err == nil {
			t.Fatalf("missing %s should fail", drop)
		}
	}
	// Content failures surface at provider init, not at resolve. Trailing data
	// after the PEM block is ignored by design (chains and footers survive).
	for _, bad := range []SSOConfig{
		{ID: "s", Provider: "saml", EntityID: "e", IdPSSOURL: "ftp://idp.example.com/sso", IdPCert: pemCert},
		{ID: "s", Provider: "saml", EntityID: "e", IdPSSOURL: "https://idp.example.com/sso", IdPCert: "not a pem"},
	} {
		if _, err := NewSSOProvider(t.Context(), &bad); err == nil {
			t.Fatalf("%+v should fail init", bad)
		}
	}
}

func TestSAMLLoginRedirect(t *testing.T) {
	_, pemCert := makeSAMLIdP(t)
	m := &Manager{secret: ssoTestSecret, ttl: time.Hour}
	p := samlTestProvider(t, pemCert)
	m.sso = []*ssoProvider{p}
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/auth/sso/saml/login", nil)
	c.Request.Host = "registry.example.com"
	c.Params = gin.Params{{Key: "id", Value: "saml"}}
	// Note: callback derives http:// from a plain test request.
	m.SSOLoginHandler(c)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d", w.Code)
	}
	loc, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != "https://idp.example.com/sso" {
		t.Fatalf("location = %q", w.Header().Get("Location"))
	}
	q := loc.Query()
	if q.Get("SAMLRequest") == "" || q.Get("RelayState") == "" {
		t.Fatalf("params = %v", q)
	}
	if _, _, err := parseSSOState(ssoTestSecret, q.Get("RelayState")); err != nil {
		t.Fatalf("relay state is not ours: %v", err)
	}
}

// TestSAMLCallback drives the whole assertion flow against a signed response
// from a fake IdP key: signature, audience, recipient and time window are all
// enforced by the library on the way in.
func TestSAMLCallback(t *testing.T) {
	ks, pemCert := makeSAMLIdP(t)
	m := &Manager{secret: ssoTestSecret, ttl: time.Hour}
	p := samlTestProvider(t, pemCert)
	m.sso = []*ssoProvider{p}

	now := time.Now()
	encoded := signSAMLAssertion(t, ks, samlTestEntity, samlTestCallback, "alice@example.com",
		map[string][]string{"groups": {"devs", "admins"}}, now.Add(-5*time.Minute), now.Add(5*time.Minute))
	st, err := ssoState(ssoTestSecret, "saml", "unused-verifier")
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"SAMLResponse": {encoded}, "RelayState": {st}}.Encode()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/auth/sso/saml/callback", strings.NewReader(form))
	c.Request.Host = "registry.example.com"
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Params = gin.Params{{Key: "id", Value: "saml"}}
	m.SSOCallbackHandler(c)
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/#sso_token=") {
		t.Fatalf("location = %q", loc)
	}
	claims, err := verifyToken(ssoTestSecret, strings.TrimPrefix(loc, "/#sso_token="))
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "alice@example.com" || claims["adm"] != true {
		t.Fatalf("claims = %v", claims)
	}

	// Tampered assertions do not pass.
	bad := encoded[:len(encoded)-8] + "AAAAAAAA"
	form = url.Values{"SAMLResponse": {bad}, "RelayState": {st}}.Encode()
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/auth/sso/saml/callback", strings.NewReader(form))
	c.Request.Host = "registry.example.com"
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Params = gin.Params{{Key: "id", Value: "saml"}}
	m.SSOCallbackHandler(c)
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/#sso_error=") {
		t.Fatalf("tampered location = %q", loc)
	}

	// Wrong audience does not pass either.
	other := signSAMLAssertion(t, ks, "https://someone-else.example.com", samlTestCallback, "bob",
		nil, now.Add(-5*time.Minute), now.Add(5*time.Minute))
	form = url.Values{"SAMLResponse": {other}, "RelayState": {st}}.Encode()
	w = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/auth/sso/saml/callback", strings.NewReader(form))
	c.Request.Host = "registry.example.com"
	c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.Params = gin.Params{{Key: "id", Value: "saml"}}
	m.SSOCallbackHandler(c)
	if loc := w.Header().Get("Location"); !strings.HasPrefix(loc, "/#sso_error=") {
		t.Fatalf("wrong-audience location = %q", loc)
	}
}

func TestSAMLMapping(t *testing.T) {
	ks, pemCert := makeSAMLIdP(t)
	now := time.Now()
	p := samlTestProvider(t, pemCert)

	// NameID fallback plus groups and admin mapping.
	u, err := p.validateSAMLResponse(t.Context(), samlTestCallback,
		signSAMLAssertion(t, ks, samlTestEntity, samlTestCallback, "carol@example.com",
			map[string][]string{"groups": {"ops", "admins"}}, now.Add(-time.Minute), now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if u.Name != "carol@example.com" || !u.Admin || len(u.Groups) != 2 || u.Source != "sso:saml" {
		t.Fatalf("user = %+v", u)
	}

	// Explicit username attribute wins over NameID.
	p.cfg.Username = "email"
	u, err = p.validateSAMLResponse(t.Context(), samlTestCallback,
		signSAMLAssertion(t, ks, samlTestEntity, samlTestCallback, "transient-id",
			map[string][]string{"email": {"dave@example.com"}}, now.Add(-time.Minute), now.Add(time.Minute)))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if u.Name != "dave@example.com" || u.Admin {
		t.Fatalf("user = %+v", u)
	}
}
