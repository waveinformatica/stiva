package blobstore

import (
	"context"
	"encoding/json"
	"testing"

	"registry/internal/vault"
)

// TestValidateUnion pins the discriminated union: the config block must match
// the declared kind, and only that one may be present.
func TestValidateUnion(t *testing.T) {
	ok := &Store{Name: "minio", Kind: KindS3, S3: &S3Config{Bucket: "registry", SecretKey: vault.NewRef("minio-rw")}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("store valido rifiutato: %v", err)
	}

	cases := []struct {
		name  string
		store *Store
	}{
		{"kind senza configurazione", &Store{Name: "x", Kind: KindS3}},
		{"configurazione di un altro kind", &Store{Name: "x", Kind: KindFile, File: &FileConfig{Root: "/data"}, S3: &S3Config{Bucket: "b"}}},
		{"kind sconosciuto", &Store{Name: "x", Kind: "sftp"}},
		{"nome vuoto", &Store{Name: "", Kind: KindFile, File: &FileConfig{Root: "/data"}}},
		{"s3 senza bucket", &Store{Name: "x", Kind: KindS3, S3: &S3Config{}}},
		{"file senza root", &Store{Name: "x", Kind: KindFile, File: &FileConfig{}}},
	}
	for _, c := range cases {
		if err := c.store.Validate(); err == nil {
			t.Errorf("%s: atteso rifiuto", c.name)
		}
	}
}

// TestSecretRefsAreDeclaredByType is the property that replaces scanning a
// generic JSON for field names: what is secret comes from the typed struct.
func TestSecretRefsAreDeclaredByType(t *testing.T) {
	s3 := &Store{Name: "a", Kind: KindS3, S3: &S3Config{Bucket: "b", SecretKey: vault.NewRef("k")}}
	if refs := s3.SecretRefs(); len(refs) != 1 || refs[0].Key() != "k" {
		t.Fatalf("s3: %v", refs)
	}
	// Workload identity: no stored credential at all.
	s3none := &Store{Name: "a", Kind: KindS3, S3: &S3Config{Bucket: "b"}}
	if refs := s3none.SecretRefs(); len(refs) != 0 {
		t.Fatalf("senza secret key non ci sono riferimenti: %v", refs)
	}
	file := &Store{Name: "f", Kind: KindFile, File: &FileConfig{Root: "/data"}}
	if refs := file.SecretRefs(); len(refs) != 0 {
		t.Fatalf("file: %v", refs)
	}
}

// TestStoreRejectsPlaintextCredential is the end-to-end version of the vault
// type guarantee: a definition carrying a raw secret must not decode.
func TestStoreRejectsPlaintextCredential(t *testing.T) {
	raw := `{"name":"minio","kind":"s3","s3":{"bucket":"registry","secret_key":"AbX2smEHVDjHCRFo"}}`
	var s Store
	if err := json.Unmarshal([]byte(raw), &s); err == nil {
		t.Fatalf("una credenziale in chiaro non deve entrare nella definizione (ottenuto %q)", s.S3.SecretKey)
	}

	good := `{"name":"minio","kind":"s3","s3":{"bucket":"registry","secret_key":"vault://minio-rw"}}`
	if err := json.Unmarshal([]byte(good), &s); err != nil {
		t.Fatalf("riferimento valido rifiutato: %v", err)
	}
	if s.S3.SecretKey.Key() != "minio-rw" {
		t.Fatalf("riferimento %q", s.S3.SecretKey)
	}
}

// TestJoinPrefix covers the isolation that lets several registries share one
// store. The empty case matters most: registries created before per-registry
// prefixes keep their objects exactly where they already are.
func TestJoinPrefix(t *testing.T) {
	cases := []struct{ base, reg, want string }{
		{"", "", ""},                          // layout legacy, invariato
		{"", "docker", "docker"},              // nuovo registry su store nudo
		{"team", "", "team"},                  // store con base, registry legacy
		{"team", "docker", "team/docker"},     // entrambi
		{"/team/", "/docker/", "team/docker"}, // le chiavi non hanno barra iniziale
	}
	for _, c := range cases {
		if got := joinPrefix(c.base, c.reg); got != c.want {
			t.Errorf("joinPrefix(%q,%q) = %q, atteso %q", c.base, c.reg, got, c.want)
		}
	}

	// Un percorso su filesystem deve restare assoluto: toglierne la barra
	// iniziale farebbe scrivere lo store accanto alla working directory.
	paths := []struct{ root, reg, want string }{
		{"/data/blobs", "alpha", "/data/blobs/alpha"},
		{"/data/blobs", "", "/data/blobs"},
		{"relativo", "alpha", "relativo/alpha"},
	}
	for _, c := range paths {
		if got := joinPath(c.root, c.reg); got != c.want {
			t.Errorf("joinPath(%q,%q) = %q, atteso %q", c.root, c.reg, got, c.want)
		}
	}
}

// TestResolveIsolatesRegistries is the property the prefix exists for: two
// registries on one store must not address the same objects, or deleting a blob
// in one silently breaks the other.
func TestResolveIsolatesRegistries(t *testing.T) {
	v, err := vault.New(nil, "")
	if err != nil {
		t.Fatal(err)
	}
	st := &Store{Name: "minio", Kind: KindS3, S3: &S3Config{Bucket: "registry"}}

	a, err := st.Resolve(context.Background(), v, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.Resolve(context.Background(), v, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if a.S3Prefix == b.S3Prefix {
		t.Fatalf("due registry sullo stesso store condividono il prefisso %q", a.S3Prefix)
	}
	if a.S3Bucket != b.S3Bucket {
		t.Fatal("devono comunque condividere il bucket")
	}

	// Un file store isola per directory.
	fs := &Store{Name: "local", Kind: KindFile, File: &FileConfig{Root: "/data/blobs"}}
	f, err := fs.Resolve(context.Background(), v, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if f.Root != "/data/blobs/alpha" {
		t.Fatalf("root = %q", f.Root)
	}
}
