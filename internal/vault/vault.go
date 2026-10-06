// Package vault stores credentials encrypted at rest and hands out references
// to them rather than the values themselves.
//
// Configuration structs never hold a plaintext credential: they hold a
// [SecretRef], a distinct type that refuses to decode anything other than a
// "vault://<key>" reference. That makes the struct itself the declaration of
// what is sensitive — rename a field and the code handling it has to change
// with it — and removes the ambiguous state where a field might hold either a
// value or a reference and every reader has to check which.
//
// Encryption is envelope-based: each secret carries its own random data key
// which encrypts the value, and that data key is wrapped with the master key.
// Rotating the master key re-wraps data keys; it does not touch the values.
package vault

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Prefix marks a string as a reference into the vault rather than a value.
const Prefix = "vault://"

// Derivation scopes partition wrapping keys by credential class, so a data
// key sealed for one class never opens under another — even for the same
// entry name.
const (
	// ScopeCredentials is generic named credentials (blob store keys, …).
	ScopeCredentials = "credentials"
	// ScopeSSO is SSO provider client secrets.
	ScopeSSO = "sso"
)

var (
	// ErrNotFound is returned when a reference points at a missing entry.
	ErrNotFound = errors.New("vault: secret not found")
	// ErrExists is returned when creating an entry whose key is taken.
	ErrExists = errors.New("vault: secret already exists")
	// ErrNoMasterKey is returned when the vault is used without a master key.
	ErrNoMasterKey = errors.New("vault: master key not configured (REGISTRY_VAULT_KEY)")
	// ErrInvalidRef is returned when a value is not a well-formed reference.
	ErrInvalidRef = errors.New("vault: not a secret reference")
)

// SecretRef points at a vault entry. It is deliberately not a plain string:
// unmarshalling rejects anything that is not a reference, so a cleartext
// credential cannot enter a configuration struct by accident and therefore
// cannot be serialised back out into a stored row.
type SecretRef string

// NewRef builds a reference to key.
func NewRef(key string) SecretRef { return SecretRef(Prefix + key) }

// Key returns the entry name this reference points at, or "" if unset.
func (r SecretRef) Key() string {
	if r == "" {
		return ""
	}
	return strings.TrimPrefix(string(r), Prefix)
}

// Empty reports whether the reference is unset. An unset reference is valid:
// not every credential field is mandatory.
func (r SecretRef) Empty() bool { return r == "" }

// Valid reports whether the reference is either unset or well-formed.
func (r SecretRef) Valid() bool {
	return r == "" || (strings.HasPrefix(string(r), Prefix) && len(r) > len(Prefix))
}

func (r SecretRef) String() string { return string(r) }

// MarshalJSON writes the reference as-is; there is no value to leak here.
func (r SecretRef) MarshalJSON() ([]byte, error) { return json.Marshal(string(r)) }

// UnmarshalJSON accepts only an empty string or a "vault://<key>" reference.
// A cleartext value is an error rather than something silently stored.
func (r *SecretRef) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s != "" && !strings.HasPrefix(s, Prefix) {
		return fmt.Errorf("%w: expected %q prefix", ErrInvalidRef, Prefix)
	}
	if s == Prefix {
		return fmt.Errorf("%w: empty key", ErrInvalidRef)
	}
	*r = SecretRef(s)
	return nil
}

// Entry is the metadata of a stored credential. It never carries the value:
// the API can list and describe credentials, and can never read one back.
type Entry struct {
	Key         string         `json:"key"`
	Description string         `json:"description"`
	Public      map[string]any `json:"public"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

// Vault stores and resolves credentials.
type Vault struct {
	pool      *pgxpool.Pool
	masterKey []byte
}

// New builds a vault. masterKeyHex is a hex-encoded 16, 24 or 32 byte AES key;
// when empty the vault refuses every operation rather than falling back to
// storing values unprotected.
func New(pool *pgxpool.Pool, masterKeyHex string) (*Vault, error) {
	v := &Vault{pool: pool}
	if masterKeyHex == "" {
		return v, nil
	}
	k, err := hex.DecodeString(strings.TrimSpace(masterKeyHex))
	if err != nil {
		return nil, fmt.Errorf("vault: master key is not valid hex: %w", err)
	}
	switch len(k) {
	case 16, 24, 32:
	default:
		return nil, fmt.Errorf("vault: master key must be 16, 24 or 32 bytes, got %d", len(k))
	}
	v.masterKey = k
	return v, nil
}

// Enabled reports whether a master key is configured.
func (v *Vault) Enabled() bool { return len(v.masterKey) > 0 }

func encrypt(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func decrypt(key, ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("vault: ciphertext too short")
	}
	nonce, body := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, body, nil)
}

// deriveWrapKey derives the key-encryption key for one entry from the master
// key and the entry's scope and name: KEK = HMAC(master, domain || scope ||
// name). Entries sealed under different names need different keys, so a
// wrapped data key copied to another entry never unwraps — the ciphertext is
// bound to its name. The output is always 32 bytes (AES-256), whatever the
// master key size (16, 24 or 32 bytes).
func deriveWrapKey(master []byte, scope, name string) []byte {
	m := hmac.New(sha256.New, master)
	m.Write([]byte("vault-wrap/v1\x00"))
	m.Write([]byte(scope))
	m.Write([]byte("\x00"))
	m.Write([]byte(name))
	return m.Sum(nil)
}

// sealWith wraps value under a fresh random data key, wrapping the data key
// with kek. Every call uses a new data key and nonce.
func sealWith(kek []byte, value string) (encKey, encValue []byte, err error) {
	dataKey := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return nil, nil, err
	}
	if encKey, err = encrypt(kek, dataKey); err != nil {
		return nil, nil, err
	}
	if encValue, err = encrypt(dataKey, []byte(value)); err != nil {
		return nil, nil, err
	}
	return encKey, encValue, nil
}

// openWith unwraps a data key with kek and decrypts the value. Anything
// sealed under another key — or tampered with — fails closed here.
func openWith(kek, encKey, encValue []byte) (string, error) {
	dataKey, err := decrypt(kek, encKey)
	if err != nil {
		return "", err
	}
	plain, err := decrypt(dataKey, encValue)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// Save creates or replaces an entry. Its data key is wrapped with the key
// derived from the master key and the entry's scope and name — the same
// envelope for every secret, in every class.
func (v *Vault) Save(ctx context.Context, scope, key, value, description string, public map[string]any) (SecretRef, error) {
	if !v.Enabled() {
		return "", ErrNoMasterKey
	}
	if key = strings.TrimSpace(key); key == "" {
		return "", errors.New("vault: key must not be empty")
	}
	encKey, encValue, err := sealWith(deriveWrapKey(v.masterKey, scope, key), value)
	if err != nil {
		return "", err
	}
	if public == nil {
		public = map[string]any{}
	}
	pub, err := json.Marshal(public)
	if err != nil {
		return "", err
	}
	if err := v.saveSealed(ctx, key, description, string(pub), encKey, encValue); err != nil {
		return "", err
	}
	return NewRef(key), nil
}

// saveSealed writes an already-sealed entry, replacing any previous one.
func (v *Vault) saveSealed(ctx context.Context, key, description, pub string, encKey, encValue []byte) error {
	_, err := v.pool.Exec(ctx,
		`INSERT INTO secrets(key, description, enc_key, enc_value, public_data)
		 VALUES($1,$2,$3,$4,$5)
		 ON CONFLICT (key) DO UPDATE
		   SET description = EXCLUDED.description,
		       enc_key     = EXCLUDED.enc_key,
		       enc_value   = EXCLUDED.enc_value,
		       public_data = EXCLUDED.public_data,
		       updated_at  = now()`,
		key, description, encKey, encValue, pub)
	return err
}

// Create is Save but refuses to overwrite an existing entry.
func (v *Vault) Create(ctx context.Context, scope, key, value, description string, public map[string]any) (SecretRef, error) {
	if !v.Enabled() {
		return "", ErrNoMasterKey
	}
	var exists bool
	if err := v.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM secrets WHERE key=$1)`, key).Scan(&exists); err != nil {
		return "", err
	}
	if exists {
		return "", ErrExists
	}
	return v.Save(ctx, scope, key, value, description, public)
}

// Resolve returns the plaintext behind a reference. An empty reference yields
// an empty string, so optional credentials need no special-casing at call sites.
// Entries sealed before scoped wrapping existed are opened with the master key
// and transparently re-wrapped under the derived key, so legacy rows converge
// to the scoped form on first read; anything else fails closed.
func (v *Vault) Resolve(ctx context.Context, scope string, ref SecretRef) (string, error) {
	if ref.Empty() {
		return "", nil
	}
	if !ref.Valid() {
		return "", ErrInvalidRef
	}
	if !v.Enabled() {
		return "", ErrNoMasterKey
	}
	encKey, encValue, err := v.fetchSealed(ctx, ref.Key())
	if err != nil {
		return "", err
	}
	if plain, err := openWith(deriveWrapKey(v.masterKey, scope, ref.Key()), encKey, encValue); err == nil {
		return plain, nil
	}
	dataKey, err := decrypt(v.masterKey, encKey)
	if err != nil {
		return "", fmt.Errorf("vault: decrypt %q: %w", ref.Key(), err)
	}
	plain, err := decrypt(dataKey, encValue)
	if err != nil {
		return "", fmt.Errorf("vault: decrypt %q: %w", ref.Key(), err)
	}
	// Best effort: a failed upgrade leaves a readable legacy row behind for
	// the next read to retry. The value ciphertext is untouched — only the
	// data-key wrapping moves to the derived key.
	if newEncKey, err := encrypt(deriveWrapKey(v.masterKey, scope, ref.Key()), dataKey); err == nil {
		_, _ = v.pool.Exec(ctx, `UPDATE secrets SET enc_key=$2, updated_at=now() WHERE key=$1`,
			ref.Key(), newEncKey)
	}
	return string(plain), nil
}

// fetchSealed reads an entry's ciphertexts.
func (v *Vault) fetchSealed(ctx context.Context, key string) (encKey, encValue []byte, err error) {
	err = v.pool.QueryRow(ctx,
		`SELECT enc_key, enc_value FROM secrets WHERE key=$1`, key).Scan(&encKey, &encValue)
	if err != nil {
		return nil, nil, ErrNotFound
	}
	return encKey, encValue, nil
}

// List returns entry metadata, never values.
func (v *Vault) List(ctx context.Context) ([]Entry, error) {
	rows, err := v.pool.Query(ctx,
		`SELECT key, description, public_data, created_at, updated_at FROM secrets ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		var e Entry
		var pub []byte
		if err := rows.Scan(&e.Key, &e.Description, &pub, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		e.Public = map[string]any{}
		if len(pub) > 0 {
			_ = json.Unmarshal(pub, &e.Public)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Get returns a single entry's metadata.
func (v *Vault) Get(ctx context.Context, key string) (*Entry, error) {
	var e Entry
	var pub []byte
	err := v.pool.QueryRow(ctx,
		`SELECT key, description, public_data, created_at, updated_at FROM secrets WHERE key=$1`, key).
		Scan(&e.Key, &e.Description, &pub, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		return nil, ErrNotFound
	}
	e.Public = map[string]any{}
	if len(pub) > 0 {
		_ = json.Unmarshal(pub, &e.Public)
	}
	return &e, nil
}

// Delete removes an entry. Callers are responsible for refusing to delete a
// credential that something still references — see storage.SecretReferences.
func (v *Vault) Delete(ctx context.Context, key string) error {
	tag, err := v.pool.Exec(ctx, `DELETE FROM secrets WHERE key=$1`, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
