package registry

import (
	"errors"
	"testing"

	"registry/internal/digest"
	"registry/internal/storage"
)

type fakeDeleter struct {
	deleted []digest.Digest
	fail    map[digest.Digest]bool
}

func (f *fakeDeleter) DeleteBlob(d digest.Digest) error {
	if f.fail[d] {
		return errors.New("backend exploded")
	}
	f.deleted = append(f.deleted, d)
	return nil
}

func orphanBlobs(names ...string) []storage.BlobInfo {
	out := make([]storage.BlobInfo, 0, len(names))
	for i, n := range names {
		out = append(out, storage.BlobInfo{Digest: digest.Digest(n), Size: int64(100 + i)})
	}
	return out
}

func TestSweepBlobsDeletesAll(t *testing.T) {
	f := &fakeDeleter{fail: map[digest.Digest]bool{}}
	deleted, bytes, errs := SweepBlobs(f, orphanBlobs("sha256:a", "sha256:b"), false)
	if deleted != 2 || bytes != 201 || len(errs) != 0 {
		t.Fatalf("deleted=%d bytes=%d errs=%v", deleted, bytes, errs)
	}
	if len(f.deleted) != 2 {
		t.Fatalf("deleter saw %v", f.deleted)
	}
}

func TestSweepBlobsDryRunDeletesNothing(t *testing.T) {
	f := &fakeDeleter{fail: map[digest.Digest]bool{}}
	deleted, bytes, errs := SweepBlobs(f, orphanBlobs("sha256:a"), true)
	if deleted != 0 || bytes != 0 || len(errs) != 0 || len(f.deleted) != 0 {
		t.Fatalf("dry run must not delete: %d %d %v %v", deleted, bytes, errs, f.deleted)
	}
}

func TestSweepBlobsContinuesOnError(t *testing.T) {
	f := &fakeDeleter{fail: map[digest.Digest]bool{"sha256:bad": true}}
	deleted, bytes, errs := SweepBlobs(f, orphanBlobs("sha256:ok", "sha256:bad", "sha256:ok2"), false)
	if deleted != 2 || len(errs) != 1 {
		t.Fatalf("deleted=%d errs=%v", deleted, errs)
	}
	if len(f.deleted) != 2 || f.deleted[0] != "sha256:ok" || f.deleted[1] != "sha256:ok2" {
		t.Fatalf("deleter saw %v", f.deleted)
	}
	_ = bytes
}

func TestSweepBlobsEmpty(t *testing.T) {
	f := &fakeDeleter{fail: map[digest.Digest]bool{}}
	if d, b, e := SweepBlobs(f, nil, false); d != 0 || b != 0 || len(e) != 0 {
		t.Fatalf("empty sweep = %d %d %v", d, b, e)
	}
}
