// Package blobstore models blob storage backends as named, first-class
// entities instead of configuration inlined into each registry.
//
// Two things follow from that. A single backend can be shared by many
// registries, so one MinIO bucket is described once rather than copied into
// every registry that uses it. And each backend kind is its own typed struct
// with its own credential fields, so what is secret is declared by the type
// rather than by a list of field names checked at runtime.
package blobstore

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"registry/internal/storage"
	"registry/internal/vault"
)

// Kind identifies a storage backend.
type Kind string

const (
	KindFile  Kind = "file"
	KindS3    Kind = "s3"
	KindGCS   Kind = "gcs"
	KindAzure Kind = "azure"
)

// Kinds lists the supported backends, in the order the UI should offer them.
var Kinds = []Kind{KindFile, KindS3, KindGCS, KindAzure}

var (
	ErrNotFound = errors.New("blobstore: not found")
	ErrExists   = errors.New("blobstore: already exists")
	ErrInUse    = errors.New("blobstore: still referenced by a registry")
)

// FileConfig stores blobs on the node filesystem. Note that this ties every
// registry using it to a single node.
type FileConfig struct {
	Root string `json:"root"`
}

// S3Config covers AWS S3 and S3-compatible servers (MinIO, Ceph).
//
// AccessKeyID is not secret and stays in cleartext so the store can be
// identified at a glance; SecretKey is a vault reference. When SecretKey is
// unset the AWS default credential chain applies (environment, IRSA, instance
// role), which is how a store backed by workload identity is expressed.
type S3Config struct {
	Bucket         string          `json:"bucket"`
	Region         string          `json:"region"`
	Endpoint       string          `json:"endpoint"`
	Prefix         string          `json:"prefix"`
	ForcePathStyle bool            `json:"force_path_style"`
	AccessKeyID    string          `json:"access_key_id"`
	SecretKey      vault.SecretRef `json:"secret_key"`
}

// GCSConfig covers Google Cloud Storage. CredentialsFile is a path, not a
// secret; leaving it empty uses application default credentials.
type GCSConfig struct {
	Bucket          string `json:"bucket"`
	Prefix          string `json:"prefix"`
	CredentialsFile string `json:"credentials_file"`
}

// AzureConfig covers Azure Blob Storage. The connection string carries the
// account key, so it is always a vault reference.
type AzureConfig struct {
	Container        string          `json:"container"`
	Prefix           string          `json:"prefix"`
	ConnectionString vault.SecretRef `json:"connection_string"`
}

// Store is a named storage backend. Exactly one of the per-kind configs is set,
// matching Kind — see Validate.
type Store struct {
	Name        string `json:"name"`
	Kind        Kind   `json:"kind"`
	Description string `json:"description"`

	File  *FileConfig  `json:"file,omitempty"`
	S3    *S3Config    `json:"s3,omitempty"`
	GCS   *GCSConfig   `json:"gcs,omitempty"`
	Azure *AzureConfig `json:"azure,omitempty"`
}

// Validate checks the discriminated union holds together: the config matching
// Kind is present, and no other one is.
func (s *Store) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("blobstore: name must not be empty")
	}
	set := map[Kind]bool{
		KindFile:  s.File != nil,
		KindS3:    s.S3 != nil,
		KindGCS:   s.GCS != nil,
		KindAzure: s.Azure != nil,
	}
	if !set[s.Kind] {
		return fmt.Errorf("blobstore: kind %q requires its own configuration block", s.Kind)
	}
	for k, present := range set {
		if k != s.Kind && present {
			return fmt.Errorf("blobstore: kind is %q but %q configuration is also set", s.Kind, k)
		}
	}
	switch s.Kind {
	case KindFile:
		if s.File.Root == "" {
			return errors.New("blobstore: file store requires a root path")
		}
	case KindS3:
		if s.S3.Bucket == "" {
			return errors.New("blobstore: s3 store requires a bucket")
		}
		if !s.S3.SecretKey.Valid() {
			return vault.ErrInvalidRef
		}
	case KindGCS:
		if s.GCS.Bucket == "" {
			return errors.New("blobstore: gcs store requires a bucket")
		}
	case KindAzure:
		if s.Azure.Container == "" {
			return errors.New("blobstore: azure store requires a container")
		}
		if !s.Azure.ConnectionString.Valid() {
			return vault.ErrInvalidRef
		}
	default:
		return fmt.Errorf("blobstore: unknown kind %q", s.Kind)
	}
	return nil
}

// SecretRefs lists the vault entries this store depends on. It is what makes
// "refuse to delete a credential that is still in use" possible, and it is
// derived from the typed fields rather than from a scan over generic JSON.
func (s *Store) SecretRefs() []vault.SecretRef {
	var out []vault.SecretRef
	switch s.Kind {
	case KindS3:
		if s.S3 != nil && !s.S3.SecretKey.Empty() {
			out = append(out, s.S3.SecretKey)
		}
	case KindAzure:
		if s.Azure != nil && !s.Azure.ConnectionString.Empty() {
			out = append(out, s.Azure.ConnectionString)
		}
	}
	return out
}

// joinPrefix appends the per-registry prefix to an object-store key prefix.
// Keys have no leading slash, so both parts are trimmed; an empty result keeps
// the layout a registry created before per-registry prefixes already has.
func joinPrefix(base, registry string) string {
	base, registry = strings.Trim(base, "/"), strings.Trim(registry, "/")
	switch {
	case base == "":
		return registry
	case registry == "":
		return base
	default:
		return base + "/" + registry
	}
}

// joinPath is the filesystem counterpart. It must not be confused with
// joinPrefix: trimming a leading slash here would turn an absolute root into a
// relative one, and the store would silently write beside the process working
// directory instead of where it was configured.
func joinPath(root, registry string) string {
	registry = strings.Trim(registry, "/")
	if registry == "" {
		return root
	}
	return path.Join(root, registry)
}

// Resolve turns the stored definition into a runtime configuration for one
// registry, decrypting the referenced credentials. prefix isolates that
// registry's objects inside a store shared with others. The result is passed straight to the backend
// constructor and never persisted: plaintext exists only in memory, for as long
// as it takes to build the client.
func (s *Store) Resolve(ctx context.Context, v *vault.Vault, prefix string) (storage.BlobConfig, error) {
	cfg := storage.BlobConfig{Type: string(s.Kind)}
	switch s.Kind {
	case KindFile:
		cfg.Root = joinPath(s.File.Root, prefix)
	case KindS3:
		secret, err := v.Resolve(ctx, vault.ScopeCredentials, s.S3.SecretKey)
		if err != nil {
			return cfg, fmt.Errorf("blobstore %q: %w", s.Name, err)
		}
		cfg.S3Bucket = s.S3.Bucket
		cfg.S3Region = s.S3.Region
		cfg.S3Endpoint = s.S3.Endpoint
		cfg.S3Prefix = joinPrefix(s.S3.Prefix, prefix)
		cfg.S3ForcePathStyle = s.S3.ForcePathStyle
		cfg.S3AccessKey = s.S3.AccessKeyID
		cfg.S3SecretKey = secret
	case KindGCS:
		cfg.GCSBucket = s.GCS.Bucket
		cfg.GCSPrefix = joinPrefix(s.GCS.Prefix, prefix)
		cfg.GCSCredentialsFile = s.GCS.CredentialsFile
	case KindAzure:
		conn, err := v.Resolve(ctx, vault.ScopeCredentials, s.Azure.ConnectionString)
		if err != nil {
			return cfg, fmt.Errorf("blobstore %q: %w", s.Name, err)
		}
		cfg.AzureContainer = s.Azure.Container
		cfg.AzurePrefix = joinPrefix(s.Azure.Prefix, prefix)
		cfg.AzureConnectionString = conn
	default:
		return cfg, fmt.Errorf("blobstore: unknown kind %q", s.Kind)
	}
	return cfg, nil
}
