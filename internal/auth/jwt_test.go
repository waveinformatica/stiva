package auth

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// forge builds a token signed with secret but with arbitrary claims, to reach
// verifyToken paths that issueToken never produces.
func forge(secret []byte, claims map[string]any) string {
	hb, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	in := b64(hb) + "." + b64(pb)
	return in + "." + hmacSum(secret, in)
}

func TestVerifyTokenRequiresExpiry(t *testing.T) {
	secret := []byte("secret-di-prova")

	cases := []struct {
		name   string
		token  string
		accept bool
	}{
		{
			name:   "token valido",
			token:  forge(secret, map[string]any{"sub": "alice", "exp": time.Now().Add(time.Hour).Unix()}),
			accept: true,
		},
		{
			// The fix: previously this was accepted and never expired.
			name:   "senza exp",
			token:  forge(secret, map[string]any{"sub": "mallory"}),
			accept: false,
		},
		{
			name:   "exp non numerico",
			token:  forge(secret, map[string]any{"sub": "mallory", "exp": "9999999999"}),
			accept: false,
		},
		{
			name:   "scaduto",
			token:  forge(secret, map[string]any{"sub": "alice", "exp": time.Now().Add(-time.Minute).Unix()}),
			accept: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := verifyToken(secret, tc.token)
			if tc.accept && err != nil {
				t.Fatalf("atteso accettato, rifiutato: %v", err)
			}
			if !tc.accept && err == nil {
				t.Fatal("atteso rifiutato, accettato")
			}
		})
	}
}

// TestVerifyTokenRejectsForgery covers the signature checks around the expiry
// change, so tightening exp did not loosen anything else.
func TestVerifyTokenRejectsForgery(t *testing.T) {
	secret := []byte("secret-di-prova")
	claims := map[string]any{"sub": "alice", "adm": true, "exp": time.Now().Add(time.Hour).Unix()}

	if _, err := verifyToken(secret, forge([]byte("secret-sbagliato"), claims)); err == nil {
		t.Fatal("un token firmato con un altro secret deve essere rifiutato")
	}

	// "alg": "none" with an empty signature: rejected because verifyToken
	// ignores the header and always requires a valid HMAC.
	hb, _ := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	pb, _ := json.Marshal(claims)
	if _, err := verifyToken(secret, b64(hb)+"."+b64(pb)+"."); err == nil {
		t.Fatal("alg:none deve essere rifiutato")
	}

	// Payload swapped after signing.
	good := forge(secret, map[string]any{"sub": "bob", "exp": time.Now().Add(time.Hour).Unix()})
	evil, _ := json.Marshal(map[string]any{"sub": "bob", "adm": true, "exp": time.Now().Add(time.Hour).Unix()})
	tampered := b64([]byte(`{"alg":"HS256","typ":"JWT"}`)) + "." +
		base64.RawURLEncoding.EncodeToString(evil) + "." + good[len(good)-43:]
	if _, err := verifyToken(secret, tampered); err == nil {
		t.Fatal("un payload manomesso deve essere rifiutato")
	}
}
