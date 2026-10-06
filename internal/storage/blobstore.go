package storage

import (
	"fmt"
	"strings"
)

// NewBlobStore builds a BlobStore from configuration. Supported types mirror the
// object stores offered by Nexus and Artifactory: file, S3, GCS, Azure Blob.
func NewBlobStore(cfg BlobConfig) (BlobStore, error) {
	switch strings.ToLower(cfg.Type) {
	case "", "file", "filesystem":
		if cfg.Root == "" {
			return nil, fmt.Errorf("blob: file store requires 'root'")
		}
		return NewFSBlobStore(cfg.Root)
	case "s3":
		return NewS3BlobStore(cfg)
	case "gcs":
		return NewGCSBlobStore(cfg)
	case "azure":
		return NewAzureBlobStore(cfg)
	default:
		return nil, fmt.Errorf("blob: unknown store type %q", cfg.Type)
	}
}

// objectKey returns the storage key for a digest, honoring an optional prefix.
func objectKey(prefix, algorithm, hex string) string {
	k := algorithm + "/" + hex
	if prefix != "" {
		k = strings.Trim(prefix, "/") + "/" + k
	}
	return k
}
