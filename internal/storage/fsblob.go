package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"

	"registry/internal/digest"
)

// ErrNotFound is returned when a requested object does not exist.
var ErrNotFound = errors.New("registry/storage: not found")

// ErrBlobExists is returned when attempting to overwrite an existing blob.
var ErrBlobExists = errors.New("registry/storage: blob already exists")

// BlobStore is a content-addressable blob store.
type BlobStore interface {
	// Get returns a reader for the blob and its size.
	Get(d digest.Digest) (io.ReadCloser, int64, error)
	// Put writes a blob, verifying the digest matches.
	Put(d digest.Digest, r io.Reader) error
	// Stat returns the size of a blob.
	Stat(d digest.Digest) (int64, error)
	// Exists reports whether a blob is present.
	Exists(d digest.Digest) bool
	// Delete removes a blob.
	Delete(d digest.Digest) error
}

// FSBlobStore stores blobs on the local filesystem, sharded by algorithm+prefix.
type FSBlobStore struct {
	root string
}

// NewFSBlobStore creates a filesystem-backed blob store rooted at root/blobs.
func NewFSBlobStore(root string) (*FSBlobStore, error) {
	dir := filepath.Join(root, "blobs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &FSBlobStore{root: dir}, nil
}

func (s *FSBlobStore) path(d digest.Digest) string {
	// blobs/sha256/<hex>
	return filepath.Join(s.root, string(d.Algorithm()), d.Hex())
}

func (s *FSBlobStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	f, err := os.Open(s.path(d))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

func (s *FSBlobStore) Put(d digest.Digest, r io.Reader) error {
	if s.Exists(d) {
		return ErrBlobExists
	}
	dir := filepath.Join(s.root, string(d.Algorithm()))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	h := d.Algorithm().NewHash()
	_, err = io.Copy(tmp, io.TeeReader(r, h))
	if err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err = tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}

	// Verify digest.
	sum := h.Sum(nil)
	computed := digest.Digest(string(d.Algorithm()) + ":" + digest.EncodeHex(sum))
	if computed != d {
		os.Remove(tmpName)
		return errors.New("registry/storage: digest mismatch on write")
	}

	final := s.path(d)
	if err = os.Rename(tmpName, final); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

func (s *FSBlobStore) Stat(d digest.Digest) (int64, error) {
	info, err := os.Stat(s.path(d))
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return info.Size(), nil
}

func (s *FSBlobStore) Exists(d digest.Digest) bool {
	_, err := os.Stat(s.path(d))
	return err == nil
}

func (s *FSBlobStore) Delete(d digest.Digest) error {
	err := os.Remove(s.path(d))
	if os.IsNotExist(err) {
		return ErrNotFound
	}
	return err
}
