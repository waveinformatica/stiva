package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// ErrUnauthorized is returned when no realm authenticates the credentials.
var ErrUnauthorized = errors.New("auth: unauthorized")

// ErrNoMatch signals a realm did not handle the credential (try the next one).
var ErrNoMatch = errors.New("auth: realm did not match")

// issueToken produces an HMAC-signed JWT (HS256) carrying the user identity.
// keyHash binds the session to the API key it was issued from (empty for
// normal logins): every request re-resolves the key, so revocation and grant
// changes apply immediately instead of lingering until expiry.
func issueToken(secret []byte, user *User, ttl time.Duration, keyHash string) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	payload := map[string]any{
		"sub": user.Name,
		"src": user.Source,
		"adm": user.Admin,
		"exp": time.Now().Add(ttl).Unix(),
		"iat": time.Now().Unix(),
	}
	if user.PasswordChangeRequired {
		payload["pcr"] = true
	}
	if len(user.Groups) > 0 {
		payload["groups"] = user.Groups
	}
	if keyHash != "" {
		payload["key"] = keyHash
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	pb, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	signingInput := b64(hb) + "." + b64(pb)
	sig := hmacSum(secret, signingInput)
	return signingInput + "." + sig, nil
}

// verifyToken validates an HMAC-signed JWT and returns its claims.
func verifyToken(secret []byte, token string) (map[string]any, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrUnauthorized
	}
	signingInput := parts[0] + "." + parts[1]
	expected := hmacSum(secret, signingInput)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, ErrUnauthorized
	}
	pb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrUnauthorized
	}
	var claims map[string]any
	if err := json.Unmarshal(pb, &claims); err != nil {
		return nil, ErrUnauthorized
	}
	// Expiry is mandatory: a token without a usable "exp" is rejected rather
	// than treated as non-expiring, so the check fails closed.
	exp, ok := claims["exp"].(float64)
	if !ok {
		return nil, ErrUnauthorized
	}
	if time.Now().Unix() > int64(exp) {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func hmacSum(secret []byte, input string) string {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(input))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}
