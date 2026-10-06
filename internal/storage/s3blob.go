package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"

	"registry/internal/digest"
)

// S3BlobStore stores blobs in an AWS S3 (or compatible, e.g. MinIO) bucket.
type S3BlobStore struct {
	client *s3.Client
	bucket string
	prefix string
}

// NewS3BlobStore creates an S3-backed BlobStore. Supports custom endpoints and
// path-style addressing for S3-compatible servers.
func NewS3BlobStore(cfg BlobConfig) (*S3BlobStore, error) {
	if cfg.S3Bucket == "" {
		return nil, errors.New("blob: s3 requires bucket")
	}
	opts := []func(*awsconfig.LoadOptions) error{
		// The SDK defaults to computing a CRC32 trailing checksum, which it can
		// only do over TLS or from a seekable stream. Blobs are streamed to the
		// store, and S3-compatible servers (MinIO, Ceph) are commonly reached
		// over plaintext inside a cluster, so the default makes every upload
		// fail with "unseekable stream is not supported without TLS". Integrity
		// is not weakened: this store is content-addressed and every write is
		// verified against its digest below.
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	}
	if cfg.S3Region != "" {
		opts = append(opts, awsconfig.WithRegion(cfg.S3Region))
	}
	if cfg.S3AccessKey != "" {
		opts = append(opts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, "")))
	}
	awscfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	clientOpts := []func(*s3.Options){}
	if cfg.S3ForcePathStyle {
		clientOpts = append(clientOpts, func(o *s3.Options) { o.UsePathStyle = true })
	}
	if cfg.S3Endpoint != "" {
		clientOpts = append(clientOpts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
			o.UsePathStyle = true
		})
	}
	client := s3.NewFromConfig(awscfg, clientOpts...)
	return &S3BlobStore{
		client: client,
		bucket: cfg.S3Bucket,
		prefix: strings.Trim(cfg.S3Prefix, "/"),
	}, nil
}

func (s *S3BlobStore) key(d digest.Digest) string {
	return objectKey(s.prefix, d.Algorithm().String(), d.Hex())
}

func (s *S3BlobStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	out, err := s.client.GetObject(context.Background(), &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(d)),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return out.Body, aws.ToInt64(out.ContentLength), nil
}

func (s *S3BlobStore) Put(d digest.Digest, r io.Reader) error {
	if s.Exists(d) {
		return ErrBlobExists
	}
	// Stage to a temporary file before uploading. Two reasons:
	//
	//  1. SigV4 has to hash the payload before sending it, and it can only do
	//     that from a seekable body unless the endpoint is HTTPS. Wrapping the
	//     reader in io.TeeReader makes it unseekable, so streaming straight to
	//     PutObject fails with "request stream is not seekable" against the
	//     plaintext endpoints typical of in-cluster S3-compatible stores.
	//  2. It lets the digest be verified *before* anything reaches the bucket,
	//     rather than uploading and then deleting a bad object.
	tmp, err := os.CreateTemp("", "s3blob-")
	if err != nil {
		return err
	}
	defer func() {
		tmp.Close()
		os.Remove(tmp.Name())
	}()

	h := d.Algorithm().NewHash()
	if _, err := io.Copy(tmp, io.TeeReader(r, h)); err != nil {
		return err
	}
	computed := digest.Digest(d.Algorithm().String() + ":" + digest.EncodeHex(h.Sum(nil)))
	if computed != d {
		return errors.New("blob: digest mismatch on write")
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}

	_, err = s.client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(s.key(d)),
		Body:        tmp,
		ContentType: aws.String("application/octet-stream"),
	})
	return err
}

func (s *S3BlobStore) Stat(d digest.Digest) (int64, error) {
	out, err := s.client.HeadObject(context.Background(), &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(d)),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return aws.ToInt64(out.ContentLength), nil
}

func (s *S3BlobStore) Exists(d digest.Digest) bool {
	_, err := s.Stat(d)
	return err == nil
}

func (s *S3BlobStore) Delete(d digest.Digest) error {
	_, err := s.client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(s.key(d)),
	})
	if err != nil {
		var nf *types.NotFound
		if errors.As(err, &nf) {
			return ErrNotFound
		}
		return err
	}
	return nil
}
