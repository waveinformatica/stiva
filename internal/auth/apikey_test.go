package auth

import "testing"

// TestAPIKeyHashing pins the properties the stored form must have: the hash is
// deterministic (so keys issued before the migration keep working), it is not
// the key itself, and distinct keys do not collide.
func TestAPIKeyHashing(t *testing.T) {
	const key = "rk_0123456789abcdef0123456789abcdef0123456789abcdef"

	h1, h2 := hashAPIKey(key), hashAPIKey(key)
	if h1 != h2 {
		t.Fatal("l'hash deve essere deterministico")
	}
	if h1 == key {
		t.Fatal("la chiave non deve essere memorizzata in chiaro")
	}
	if len(h1) != 64 {
		t.Fatalf("atteso sha256 esadecimale (64 char), ottenuti %d", len(h1))
	}
	if hashAPIKey(key) == hashAPIKey(key+"x") {
		t.Fatal("chiavi diverse non devono collidere")
	}
}

// TestMaskedRoundTrip guards the contract between what the admin UI is shown
// and what it sends back to revoke: maskedToPrefix must invert maskKey, or
// revocation silently stops matching anything.
func TestMaskedRoundTrip(t *testing.T) {
	const key = "rk_0123456789abcdef0123456789abcdef0123456789abcdef"

	prefix := apiKeyPrefix(key)
	if len(prefix) != apiKeyPrefixLen {
		t.Fatalf("prefisso di lunghezza %d, atteso %d", len(prefix), apiKeyPrefixLen)
	}
	if prefix != "rk_01234567" {
		t.Fatalf("prefisso inatteso: %q", prefix)
	}

	masked := maskKey(prefix)
	if got := maskedToPrefix(masked); got != prefix {
		t.Fatalf("round-trip rotto: maskKey(%q)=%q -> maskedToPrefix=%q", prefix, masked, got)
	}
	// The masked form must not leak the secret tail of the key.
	if len(masked) >= len(key) {
		t.Fatal("la forma mascherata non deve rivelare la chiave intera")
	}
}
