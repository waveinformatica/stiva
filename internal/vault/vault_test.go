package vault

import (
	"encoding/json"
	"testing"
)

// TestSecretRefRejectsPlaintext is the core guarantee of the type: a cleartext
// credential must not be able to enter a configuration struct, because from
// there it would be serialised straight back into a stored row.
func TestSecretRefRejectsPlaintext(t *testing.T) {
	type cfg struct {
		Bucket    string    `json:"bucket"`
		SecretKey SecretRef `json:"secret_key"`
	}

	ok := []string{`{"bucket":"b","secret_key":"vault://minio-rw"}`, `{"bucket":"b","secret_key":""}`}
	for _, in := range ok {
		var c cfg
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Errorf("atteso accettato, rifiutato: %s -> %v", in, err)
		}
	}

	bad := []string{
		`{"secret_key":"AbX2smEHVDjHCRFo1oOwWIdoylAHPmM4"}`, // il caso che conta
		`{"secret_key":"vault://"}`,
		`{"secret_key":"http://altrove"}`,
	}
	for _, in := range bad {
		var c cfg
		if err := json.Unmarshal([]byte(in), &c); err == nil {
			t.Errorf("atteso rifiutato, accettato: %s (valore %q)", in, c.SecretKey)
		}
	}
}

func TestSecretRefRoundTrip(t *testing.T) {
	r := NewRef("minio-registry-rw")
	if r.Key() != "minio-registry-rw" {
		t.Fatalf("Key() = %q", r.Key())
	}
	if r.Empty() || !r.Valid() {
		t.Fatal("un riferimento costruito deve essere valido e non vuoto")
	}
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"vault://minio-registry-rw"` {
		t.Fatalf("serializzato come %s", b)
	}
	var back SecretRef
	if err := json.Unmarshal(b, &back); err != nil || back != r {
		t.Fatalf("round-trip rotto: %v %q", err, back)
	}

	var zero SecretRef
	if !zero.Empty() || !zero.Valid() {
		t.Fatal("un riferimento non impostato è valido: non tutte le credenziali sono obbligatorie")
	}
}

// TestEnvelopeEncryption checks the crypto without touching a database: the
// value must survive a wrap/unwrap cycle under the derived wrapping key, and
// must not be recoverable with the raw master key, another scope, or a
// different master key.
func TestEnvelopeEncryption(t *testing.T) {
	v, err := New(nil, "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled() {
		t.Fatal("vault should be enabled with a master key")
	}

	const plain = "AbX2smEHVDjHCRFo1oOwWIdoylAHPmM4"
	kek := deriveWrapKey(v.masterKey, "test", "k")
	encKey, encValue, err := sealWith(kek, plain)
	if err != nil {
		t.Fatal(err)
	}

	dataKey, err := decrypt(kek, encKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decrypt(dataKey, encValue)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != plain {
		t.Fatalf("ottenuto %q", got)
	}

	// The raw master key alone must not unwrap anything: derivation is mandatory.
	if _, err := decrypt(v.masterKey, encKey); err == nil {
		t.Fatal("la master key grezza non deve poter aprire la data key")
	}

	// Two secrets with the same value must not produce the same ciphertext:
	// each gets its own data key and nonce.
	_, encValue2, err := sealWith(deriveWrapKey(v.masterKey, "test", "k"), plain)
	if err != nil {
		t.Fatal(err)
	}
	if string(encValue) == string(encValue2) {
		t.Fatal("due cifrature dello stesso valore non devono coincidere")
	}

	other, _ := New(nil, "0f0e0d0c0b0a09080706050403020100000102030405060708090a0b0c0d0e0f")
	if _, err := decrypt(deriveWrapKey(other.masterKey, "test", "k"), encKey); err == nil {
		t.Fatal("una master key diversa non deve poter aprire la data key")
	}
}

func TestMasterKeyValidation(t *testing.T) {
	if _, err := New(nil, "non-esadecimale"); err == nil {
		t.Error("una master key non esadecimale deve essere rifiutata")
	}
	if _, err := New(nil, "00010203"); err == nil {
		t.Error("una master key di lunghezza errata deve essere rifiutata")
	}
	v, err := New(nil, "")
	if err != nil || v.Enabled() {
		t.Error("senza master key il vault deve costruirsi ma restare disabilitato")
	}
}

func TestDeriveWrapKey(t *testing.T) {
	master := []byte("0123456789abcdef0123456789abcdef")
	a := deriveWrapKey(master, "sso", "github")
	if len(a) != 32 {
		t.Fatalf("derived key is %d bytes, want 32", len(a))
	}
	if string(a) == string(master) {
		t.Fatal("derived key must differ from the master key")
	}
	b := deriveWrapKey(master, "sso", "github")
	if string(a) != string(b) {
		t.Fatal("derivation must be deterministic")
	}
	for name, other := range map[string][]byte{
		"other name":   deriveWrapKey(master, "sso", "gitlab"),
		"other scope":  deriveWrapKey(master, "other", "github"),
		"other master": deriveWrapKey([]byte("ffffffffffffffffffffffffffffffff"), "sso", "github"),
	} {
		if string(a) == string(other) {
			t.Fatalf("derived key collides with %s", name)
		}
	}
	// Short master keys (AES-128/192) still yield a full AES-256 wrapping key.
	if len(deriveWrapKey([]byte("0123456789abcdef"), "sso", "x")) != 32 {
		t.Fatal("16-byte master must still derive 32 bytes")
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	kek := deriveWrapKey([]byte("0123456789abcdef0123456789abcdef"), "sso", "github")
	encKey, encValue, err := sealWith(kek, "super-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := openWith(kek, encKey, encValue)
	if err != nil || plain != "super-secret-value" {
		t.Fatalf("round trip = %q %v", plain, err)
	}
	// Fresh sealings differ (random data key and nonce).
	encKey2, encValue2, err := sealWith(kek, "super-secret-value")
	if err != nil {
		t.Fatal(err)
	}
	if string(encKey) == string(encKey2) || string(encValue) == string(encValue2) {
		t.Fatal("two sealings must differ")
	}
}

func TestSealIsolation(t *testing.T) {
	master := []byte("0123456789abcdef0123456789abcdef")
	legacyKey, legacyValue, err := sealWith(master, "v")
	if err != nil {
		t.Fatal(err)
	}
	scopedKey, scopedValue, err := sealWith(deriveWrapKey(master, "sso", "sso/github"), "v")
	if err != nil {
		t.Fatal(err)
	}
	// Scoped and legacy forms are not interchangeable in either direction.
	if _, err := openWith(master, scopedKey, scopedValue); err == nil {
		t.Fatal("legacy key must not open scoped entry")
	}
	if _, err := openWith(deriveWrapKey(master, "sso", "sso/github"), legacyKey, legacyValue); err == nil {
		t.Fatal("scoped key must not open legacy entry")
	}
	// Same value under another name or scope does not open either.
	if _, err := openWith(deriveWrapKey(master, "sso", "sso/gitlab"), scopedKey, scopedValue); err == nil {
		t.Fatal("another name must not open the entry")
	}
	if _, err := openWith(deriveWrapKey(master, "other", "sso/github"), scopedKey, scopedValue); err == nil {
		t.Fatal("another scope must not open the entry")
	}
	// Tampering fails closed.
	badValue := append([]byte(nil), scopedValue...)
	badValue[len(badValue)-1] ^= 0x01
	if _, err := openWith(deriveWrapKey(master, "sso", "sso/github"), scopedKey, badValue); err == nil {
		t.Fatal("tampered value must fail")
	}
	badKey := append([]byte(nil), scopedKey...)
	badKey[len(badKey)-1] ^= 0x01
	if _, err := openWith(deriveWrapKey(master, "sso", "sso/github"), badKey, scopedValue); err == nil {
		t.Fatal("tampered data key must fail")
	}
}
