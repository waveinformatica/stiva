package storage

import (
	"context"
	"errors"
	"io"
	"strings"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"registry/internal/digest"
)

// GCSBlobStore stores blobs in a Google Cloud Storage bucket.
type GCSBlobStore struct {
	client *storage.Client
	bucket string
	prefix string
}

// NewGCSBlobStore creates a GCS-backed BlobStore. If CredentialsFile is empty,
// the default application credentials (ADC) are used.
func NewGCSBlobStore(cfg BlobConfig) (*GCSBlobStore, error) {
	if cfg.GCSBucket == "" {
		return nil, errors.New("blob: gcs requires bucket")
	}
	var opts []option.ClientOption
	if cfg.GCSCredentialsFile != "" {
		opts = append(opts, option.WithCredentialsFile(cfg.GCSCredentialsFile))
	}
	client, err := storage.NewClient(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	return &GCSBlobStore{
		client: client,
		bucket: cfg.GCSBucket,
		prefix: strings.Trim(cfg.GCSPrefix, "/"),
	}, nil
}

func (g *GCSBlobStore) key(d digest.Digest) string {
	return objectKey(g.prefix, d.Algorithm().String(), d.Hex())
}

func (g *GCSBlobStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	ctx := context.Background()
	obj := g.client.Bucket(g.bucket).Object(g.key(d))
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	rc, err := obj.NewReader(ctx)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return rc, attrs.Size, nil
}

func (g *GCSBlobStore) Put(d digest.Digest, r io.Reader) error {
	if g.Exists(d) {
		return ErrBlobExists
	}
	ctx := context.Background()
	h := d.Algorithm().NewHash()
	body := io.TeeReader(r, h)
	w := g.client.Bucket(g.bucket).Object(g.key(d)).NewWriter(ctx)
	w.ContentType = "application/octet-stream"
	if _, err := io.Copy(w, body); err != nil {
		_ = w.Close()
		_ = g.Delete(d)
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	computed := digest.Digest(d.Algorithm().String() + ":" + digest.EncodeHex(h.Sum(nil)))
	if computed != d {
		_ = g.Delete(d)
		return errors.New("blob: digest mismatch on write")
	}
	return nil
}

func (g *GCSBlobStore) Stat(d digest.Digest) (int64, error) {
	attrs, err := g.client.Bucket(g.bucket).Object(g.key(d)).Attrs(context.Background())
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return attrs.Size, nil
}

func (g *GCSBlobStore) Exists(d digest.Digest) bool {
	_, err := g.Stat(d)
	return err == nil
}

func (g *GCSBlobStore) Delete(d digest.Digest) error {
	err := g.client.Bucket(g.bucket).Object(g.key(d)).Delete(context.Background())
	if errors.Is(err, storage.ErrObjectNotExist) {
		return ErrNotFound
	}
	return err
}
