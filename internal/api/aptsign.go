package api

import (
	"bytes"
	"context"
	"fmt"
	"path"
	"strings"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/openpgp"
	"golang.org/x/crypto/openpgp/armor"
	"golang.org/x/crypto/openpgp/clearsign"
	"golang.org/x/crypto/openpgp/packet"

	"registry/internal/registry"
	"registry/internal/vault"
)

// APT repository signing. Hosted APT registries can carry a signing key (RSA,
// generated from the admin UI, private half in the vault): the generated
// Release is then also served clearsigned as InRelease and detached as
// Release.gpg, and the public half is published as KEY.gpg for the
// signed-by= apt source option. Proxied registries pass upstream signatures
// through untouched — re-signing someone else's metadata would lie about its
// origin.

// aptSignRSABits sizes generated repository keys. 3072-bit RSA is accepted by
// every apt either old enough to matter or new enough to prefer Ed25519 (which
// old apt cannot read at all).
const aptSignRSABits = 3072

// generateAPTKey creates a repository signing identity. The public half goes
// to clients, the private half never leaves the vault.
func generateAPTKey(name string) (privateArmored, publicArmored, fingerprint string, err error) {
	entity, err := openpgp.NewEntity("Stiva APT "+name, "repository signing key", "", &packet.Config{RSABits: aptSignRSABits})
	if err != nil {
		return "", "", "", fmt.Errorf("cannot generate signing key: %w", err)
	}
	var priv bytes.Buffer
	w, err := armor.Encode(&priv, "PGP PRIVATE KEY BLOCK", nil)
	if err != nil {
		return "", "", "", err
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		return "", "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", "", err
	}
	var pub bytes.Buffer
	w, err = armor.Encode(&pub, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		return "", "", "", err
	}
	if err := entity.Serialize(w); err != nil {
		return "", "", "", err
	}
	if err := w.Close(); err != nil {
		return "", "", "", err
	}
	return priv.String(), pub.String(), aptFingerprint(entity), nil
}

// parseAPTPrivateKey loads a stored signing key. Passphrases are unsupported
// by design: the vault already guards the key, and an unattended server could
// not answer a passphrase prompt anyway.
func parseAPTPrivateKey(armored string) (*openpgp.Entity, error) {
	block, err := armor.Decode(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("cannot parse signing key: %w", err)
	}
	el, err := openpgp.ReadEntity(packet.NewReader(block.Body))
	if err != nil {
		return nil, fmt.Errorf("cannot parse signing key: %w", err)
	}
	return el, nil
}

// aptFingerprint renders the primary key fingerprint the way apt-key lists
// it: uppercase hex in groups of four.
func aptFingerprint(entity *openpgp.Entity) string {
	hex := fmt.Sprintf("%X", entity.PrimaryKey.Fingerprint)
	var out strings.Builder
	for i := 0; i < len(hex); i += 4 {
		if i > 0 {
			out.WriteByte(' ')
		}
		end := i + 4
		if end > len(hex) {
			end = len(hex)
		}
		out.WriteString(hex[i:end])
	}
	return out.String()
}

// clearsignAPT wraps data as a clearsigned message (InRelease).
func clearsignAPT(entity *openpgp.Entity, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, entity.PrivateKey, nil)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// detachSignAPT signs data detached and armored (Release.gpg).
func detachSignAPT(entity *openpgp.Entity, data []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, entity, bytes.NewReader(data), nil); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// aptVaultKey scopes the vault entry holding a registry's APT signing key.
func aptVaultKey(registry string) string { return "apt-sign/" + registry }

// aptSigningEntity loads a registry's signing key, if it has one.
func aptSigningEntity(ctx context.Context, vlt *vault.Vault, registry string) (*openpgp.Entity, error) {
	if vlt == nil {
		return nil, fmt.Errorf("no vault")
	}
	sec, err := vlt.Resolve(ctx, vault.ScopeAPTSign, vault.NewRef(aptVaultKey(registry)))
	if err != nil || sec == "" {
		return nil, fmt.Errorf("no signing key")
	}
	return parseAPTPrivateKey(sec)
}

// aptPublicKey renders the published half of a registry's signing key.
func aptPublicKey(ctx context.Context, vlt *vault.Vault, registry string) (string, error) {
	entity, err := aptSigningEntity(ctx, vlt, registry)
	if err != nil {
		return "", err
	}
	var pub bytes.Buffer
	w, err := armor.Encode(&pub, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		return "", err
	}
	if err := entity.Serialize(w); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	return pub.String(), nil
}

// aptSignedMetadata serves the signed APT documents for a hosted registry
// that carries a signing key: InRelease (clearsigned Release), Release.gpg
// (detached signature) and KEY.gpg (the public key for signed-by=). Without
// a key it reports unhandled, leaving the plain unsigned Release path alone.
func (h *Handler) aptSignedMetadata(c *gin.Context, be registry.ArtifactBackend, reg *registry.Registry, p string) ([]byte, string, bool) {
	switch path.Base(p) {
	case "KEY.gpg":
		pub, err := aptPublicKey(c.Request.Context(), h.vault, reg.Name)
		if err != nil {
			return nil, "", false
		}
		return []byte(pub), "application/pgp-keys", true
	case "InRelease", "Release.gpg":
		entity, err := aptSigningEntity(c.Request.Context(), h.vault, reg.Name)
		if err != nil {
			return nil, "", false
		}
		dir := ""
		if i := strings.LastIndex(p, "/"); i >= 0 {
			dir = p[:i+1]
		}
		rel, _, ok := generateMetadataFor(be, registry.FormatAPT, dir+"Release")
		if !ok {
			return nil, "", false
		}
		if path.Base(p) == "InRelease" {
			body, err := clearsignAPT(entity, rel)
			if err != nil {
				return nil, "", false
			}
			return body, "text/plain", true
		}
		body, err := detachSignAPT(entity, rel)
		if err != nil {
			return nil, "", false
		}
		return body, "application/pgp-signature", true
	}
	return nil, "", false
}
