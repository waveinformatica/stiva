package storage

import "registry/internal/digest"

// MetadataStore persists repository, manifest, tag and blob metadata.
// Implementations: PostgreSQL (required), with others possible behind this interface.
//
// Every method takes a "registry" argument: with multiple registries hosted by a
// single server, metadata is partitioned by registry name. There is exactly one
// way to address a piece of metadata (registry + repo + digest), so there is no
// ambiguity about which registry owns a record.
type MetadataStore interface {
	Close() error

	// Repositories
	CreateRepo(registry, name string) error
	RepoExists(registry, name string) (bool, error)
	ListRepos(registry string) ([]string, error)
	DeleteRepo(registry, name string) error

	// Blobs (metadata only; content lives in the BlobStore)
	RecordBlob(registry string, d digest.Digest, size int64) error
	BlobSize(registry string, d digest.Digest) (int64, error)
	BlobRefCount(registry string, d digest.Digest) (int, error)
	DeleteBlobMeta(registry string, d digest.Digest) error

	// Manifests
	PutManifest(registry, repo string, d digest.Digest, mediaType string, content []byte) error
	GetManifest(registry, repo string, d digest.Digest) ([]byte, string, error)
	ManifestExists(registry, repo string, d digest.Digest) (bool, error)
	DeleteManifest(registry, repo string, d digest.Digest) error
	ListManifests(registry, repo string) ([]digest.Digest, error)

	// Tags
	ResolveTag(registry, repo, tag string) (digest.Digest, error)
	SetTag(registry, repo, tag string, d digest.Digest) error
	ListTags(registry, repo string) ([]string, error)

	// Manifest -> Blob links
	LinkBlob(registry, repo string, manifest, blob digest.Digest) error
	BlobsForManifest(registry, repo string, manifest digest.Digest) ([]digest.Digest, error)

	// Registry definitions (hosted/proxy/group) configured from the UI.
	UpsertRegistry(r RegistryRecord) error
	GetRegistry(name string) (*RegistryRecord, error)
	ListRegistries() ([]RegistryRecord, error)
	DeleteRegistry(name string) error

	// Objects (path-based artifacts for helm/maven/npm and other file formats).
	RecordObject(registry, path string, d digest.Digest, size int64, contentType string) error
	GetObjectMeta(registry, path string) (digest.Digest, int64, string, error)
	DeleteObject(registry, path string) error
	ListObjects(registry, prefix string) ([]string, error)

	// SSO providers for browser login, managed from the admin UI. Only the
	// non-secret half is stored here; client secrets live in the vault under
	// key "sso/<id>" and never appear in these records.
	ListSSOProviders() ([]SSOProviderRecord, error)
	UpsertSSOProvider(r SSOProviderRecord) error
	DeleteSSOProvider(id string) error
}

// SSOProviderRecord is the stored, secret-free half of one SSO provider. The
// field names mirror the admin API so the UI edits the same shape it reads.
type SSOProviderRecord struct {
	ID           string `json:"id"`
	Label        string `json:"label"`
	Provider     string `json:"provider"`
	ClientID     string `json:"client_id"`
	Tenant       string `json:"tenant"`
	BaseURL      string `json:"base_url"`
	Issuer       string `json:"issuer"`
	AuthorizeURL string `json:"authorize_url"`
	TokenURL     string `json:"token_url"`
	UserinfoURL  string `json:"userinfo_url"`
	Scope        string `json:"scope"`
	Username     string `json:"username_claim"`
	Groups       string `json:"groups_claim"`
	Audience     string `json:"audience"`
	AdminGroup   string `json:"admin_group"`
	Enabled      bool   `json:"enabled"`
}

// RegistryRecord is a persisted registry definition. Config holds the full
// Registry JSON; Name/Format/Type are denormalized for quick listing.
type RegistryRecord struct {
	Name   string
	Format string
	Type   string
	Config []byte
}

// BlobConfig selects and configures a blob storage backend.
type BlobConfig struct {
	// Type: "file" | "s3" | "gcs" | "azure"
	Type string `json:"type"`

	// file
	Root string `json:"root"`

	// s3
	S3Bucket         string `json:"s3_bucket"`
	S3Region         string `json:"s3_region"`
	S3Endpoint       string `json:"s3_endpoint"` // custom endpoint (MinIO, etc.)
	S3Prefix         string `json:"s3_prefix"`
	S3AccessKey      string `json:"s3_access_key"`
	S3SecretKey      string `json:"s3_secret_key"`
	S3ForcePathStyle bool   `json:"s3_force_path_style"`

	// gcs
	GCSBucket          string `json:"gcs_bucket"`
	GCSPrefix          string `json:"gcs_prefix"`
	GCSCredentialsFile string `json:"gcs_credentials_file"`

	// azure
	AzureContainer        string `json:"azure_container"`
	AzurePrefix           string `json:"azure_prefix"`
	AzureConnectionString string `json:"azure_connection_string"`
}

// Config is the full storage configuration.
type Config struct {
	PostgresDSN string
	Blob        BlobConfig
	UploadsDir  string // local staging area for in-progress uploads
}
