package storage

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"sync"

	"registry/internal/digest"
)

// Member is one named backend inside a composite blob store.
type Member struct {
	Name  string
	Store BlobStore
}

// Reconciler is implemented by composite blob stores. Reconcile brings one
// blob to where the layout wants it: on every replica, or on its owning shard.
// With dryRun it only reports whether anything would change.
type Reconciler interface {
	Reconcile(d digest.Digest, dryRun bool) (changed bool, err error)
}

// stage copies r to a temp file so it can be read once per member.
func stage(r io.Reader) (path string, err error) {
	f, err := os.CreateTemp("", "composite-")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), f.Close()
}

// putFrom writes the staged file to one member; an existing blob is success.
func putFrom(m Member, d digest.Digest, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := m.Store.Put(d, f); err != nil && !errors.Is(err, ErrBlobExists) {
		return err
	}
	return nil
}

// copyBlob copies d from one member to another.
func copyBlob(from, to Member, d digest.Digest) error {
	rc, _, err := from.Store.Get(d)
	if err != nil {
		return err
	}
	defer rc.Close()
	if err := to.Store.Put(d, rc); err != nil && !errors.Is(err, ErrBlobExists) {
		return err
	}
	return nil
}

// getFirst returns the blob from the first member in order that has it.
func getFirst(order []Member, d digest.Digest) (io.ReadCloser, int64, error) {
	var firstErr error
	for _, m := range order {
		rc, sz, err := m.Store.Get(d)
		if err == nil {
			return rc, sz, nil
		}
		if !errors.Is(err, ErrNotFound) && firstErr == nil {
			firstErr = fmt.Errorf("%s: %w", m.Name, err)
		}
	}
	if firstErr != nil {
		return nil, 0, firstErr
	}
	return nil, 0, ErrNotFound
}

func statFirst(order []Member, d digest.Digest) (int64, error) {
	var firstErr error
	for _, m := range order {
		sz, err := m.Store.Stat(d)
		if err == nil {
			return sz, nil
		}
		if !errors.Is(err, ErrNotFound) && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return 0, firstErr
	}
	return 0, ErrNotFound
}

func existsAny(order []Member, d digest.Digest) bool {
	for _, m := range order {
		if m.Store.Exists(d) {
			return true
		}
	}
	return false
}

// deleteAll removes d from every member. A blob a member never had is not an
// error; any other failure is reported after all members were tried.
func deleteAll(members []Member, d digest.Digest) error {
	var errs []error
	found := false
	for _, m := range members {
		switch err := m.Store.Delete(d); {
		case err == nil:
			found = true
		case errors.Is(err, ErrNotFound):
		default:
			errs = append(errs, fmt.Errorf("%s: %w", m.Name, err))
		}
	}
	if err := errors.Join(errs...); err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// ---- replicated: every blob on every member ----

type replicatedStore struct {
	members []Member
	quorum  int
}

// NewReplicatedStore keeps a full copy of every blob on each member. A write
// succeeds once quorum members have it; the rest are brought up to date by
// Reconcile. Reads fall back across members in order.
func NewReplicatedStore(members []Member, quorum int) (BlobStore, error) {
	if len(members) < 2 {
		return nil, errors.New("blob: a replicated store needs at least two members")
	}
	if quorum <= 0 {
		quorum = len(members)/2 + 1
	}
	if quorum > len(members) {
		return nil, fmt.Errorf("blob: write quorum %d exceeds the %d members", quorum, len(members))
	}
	return &replicatedStore{members: members, quorum: quorum}, nil
}

func (r *replicatedStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	return getFirst(r.members, d)
}
func (r *replicatedStore) Stat(d digest.Digest) (int64, error) { return statFirst(r.members, d) }
func (r *replicatedStore) Exists(d digest.Digest) bool         { return existsAny(r.members, d) }
func (r *replicatedStore) Delete(d digest.Digest) error        { return deleteAll(r.members, d) }

func (r *replicatedStore) Put(d digest.Digest, src io.Reader) error {
	path, err := stage(src)
	if err != nil {
		return err
	}
	defer os.Remove(path)

	errs := make([]error, len(r.members))
	var wg sync.WaitGroup
	for i, m := range r.members {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = putFrom(m, d, path)
		}()
	}
	wg.Wait()

	ok := 0
	var failed []error
	for i, err := range errs {
		if err == nil {
			ok++
		} else {
			failed = append(failed, fmt.Errorf("%s: %w", r.members[i].Name, err))
		}
	}
	if ok < r.quorum {
		return fmt.Errorf("blob: wrote to %d of %d members, need %d: %w", ok, len(r.members), r.quorum, errors.Join(failed...))
	}
	for _, err := range failed {
		log.Printf("blob: replica write failed (quorum met, reconcile will repair): %v", err)
	}
	return nil
}

// Reconcile copies d to every member that lacks it.
func (r *replicatedStore) Reconcile(d digest.Digest, dryRun bool) (bool, error) {
	var have *Member
	var missing []Member
	for i := range r.members {
		if r.members[i].Store.Exists(d) {
			if have == nil {
				have = &r.members[i]
			}
		} else {
			missing = append(missing, r.members[i])
		}
	}
	if len(missing) == 0 {
		return false, nil
	}
	if have == nil {
		return false, fmt.Errorf("blob %s is on no member", d)
	}
	if dryRun {
		return true, nil
	}
	var errs []error
	for _, m := range missing {
		if err := copyBlob(*have, m, d); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name, err))
		}
	}
	return true, errors.Join(errs...)
}

// ---- sharded: each blob on exactly one member ----

type shardedStore struct {
	members []Member
}

// NewShardedStore spreads blobs across members by rendezvous hashing of the
// digest: each blob has one owner, and adding or removing a member moves only
// the blobs that member owns. Reads try the owner first and then the others,
// so blobs written before the membership changed stay reachable until
// Reconcile moves them.
func NewShardedStore(members []Member) (BlobStore, error) {
	if len(members) < 2 {
		return nil, errors.New("blob: a sharded store needs at least two members")
	}
	return &shardedStore{members: members}, nil
}

// order ranks the members for d, owner first. Pure function of names and digest.
func (s *shardedStore) order(d digest.Digest) []Member {
	type scored struct {
		m Member
		w uint64
	}
	sc := make([]scored, len(s.members))
	for i, m := range s.members {
		h := sha256.Sum256([]byte(m.Name + "\x00" + string(d)))
		sc[i] = scored{m, binary.BigEndian.Uint64(h[:8])}
	}
	sort.Slice(sc, func(i, j int) bool {
		if sc[i].w != sc[j].w {
			return sc[i].w > sc[j].w
		}
		return sc[i].m.Name < sc[j].m.Name
	})
	out := make([]Member, len(sc))
	for i := range sc {
		out[i] = sc[i].m
	}
	return out
}

func (s *shardedStore) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	return getFirst(s.order(d), d)
}
func (s *shardedStore) Stat(d digest.Digest) (int64, error) { return statFirst(s.order(d), d) }
func (s *shardedStore) Exists(d digest.Digest) bool         { return existsAny(s.order(d), d) }
func (s *shardedStore) Delete(d digest.Digest) error        { return deleteAll(s.members, d) }

func (s *shardedStore) Put(d digest.Digest, src io.Reader) error {
	return s.order(d)[0].Store.Put(d, src)
}

// Reconcile moves d to its owning member and removes stray copies elsewhere.
func (s *shardedStore) Reconcile(d digest.Digest, dryRun bool) (bool, error) {
	order := s.order(d)
	owner := order[0]
	onOwner := owner.Store.Exists(d)
	var strays []Member
	for _, m := range order[1:] {
		if m.Store.Exists(d) {
			strays = append(strays, m)
		}
	}
	if onOwner && len(strays) == 0 {
		return false, nil
	}
	if !onOwner && len(strays) == 0 {
		return false, fmt.Errorf("blob %s is on no member", d)
	}
	if dryRun {
		return true, nil
	}
	if !onOwner {
		if err := copyBlob(strays[0], owner, d); err != nil {
			return true, fmt.Errorf("%s: %w", owner.Name, err)
		}
	}
	// Only remove strays once the owner holds the blob.
	var errs []error
	for _, m := range strays {
		if err := m.Store.Delete(d); err != nil && !errors.Is(err, ErrNotFound) {
			errs = append(errs, fmt.Errorf("%s: %w", m.Name, err))
		}
	}
	return true, errors.Join(errs...)
}
