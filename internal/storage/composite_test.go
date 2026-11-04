package storage

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"

	"registry/internal/digest"
)

// memBlobs is an in-memory BlobStore; failPut makes writes fail.
type memBlobs struct {
	mu      sync.Mutex
	data    map[digest.Digest][]byte
	failPut bool
}

func newMem() *memBlobs { return &memBlobs{data: map[digest.Digest][]byte{}} }

func (m *memBlobs) Get(d digest.Digest) (io.ReadCloser, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.data[d]
	if !ok {
		return nil, 0, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), int64(len(b)), nil
}
func (m *memBlobs) Put(d digest.Digest, r io.Reader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failPut {
		return errors.New("disk on fire")
	}
	if _, ok := m.data[d]; ok {
		return ErrBlobExists
	}
	b, _ := io.ReadAll(r)
	if digest.FromBytes(b) != d {
		return errors.New("digest mismatch on write")
	}
	m.data[d] = b
	return nil
}
func (m *memBlobs) Stat(d digest.Digest) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if b, ok := m.data[d]; ok {
		return int64(len(b)), nil
	}
	return 0, ErrNotFound
}
func (m *memBlobs) Exists(d digest.Digest) bool { _, err := m.Stat(d); return err == nil }
func (m *memBlobs) Delete(d digest.Digest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.data[d]; !ok {
		return ErrNotFound
	}
	delete(m.data, d)
	return nil
}

func members(stores ...*memBlobs) []Member {
	var out []Member
	for i, s := range stores {
		out = append(out, Member{Name: fmt.Sprintf("m%d", i), Store: s})
	}
	return out
}

func TestReplicatedWritesEveryMemberAndReadsFallBack(t *testing.T) {
	a, b, c := newMem(), newMem(), newMem()
	st, err := NewReplicatedStore(members(a, b, c), 0)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("layer")
	d := digest.FromBytes(data)
	if err := st.Put(d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	for i, m := range []*memBlobs{a, b, c} {
		if !m.Exists(d) {
			t.Errorf("member %d lacks the blob", i)
		}
	}
	// Lose the first replica: reads still succeed from another.
	_ = a.Delete(d)
	rc, _, err := st.Get(d)
	if err != nil {
		t.Fatalf("read after losing a replica: %v", err)
	}
	rc.Close()
	if !st.Exists(d) {
		t.Error("Exists must see the surviving replicas")
	}
}

func TestReplicatedQuorum(t *testing.T) {
	data := []byte("x")
	d := digest.FromBytes(data)

	// 3 members, quorum 2 (default): one failing member is tolerated.
	a, b, c := newMem(), newMem(), newMem()
	c.failPut = true
	st, _ := NewReplicatedStore(members(a, b, c), 0)
	if err := st.Put(d, bytes.NewReader(data)); err != nil {
		t.Errorf("quorum met but write failed: %v", err)
	}
	// Two failing members: quorum lost.
	b.failPut = true
	d2 := digest.FromBytes([]byte("y"))
	if err := st.Put(d2, bytes.NewReader([]byte("y"))); err == nil {
		t.Error("write succeeded without a quorum")
	}
	if _, err := NewReplicatedStore(members(a, b), 3); err == nil {
		t.Error("quorum above member count accepted")
	}
	if _, err := NewReplicatedStore(members(a), 1); err == nil {
		t.Error("single-member replicated store accepted")
	}
}

func TestReplicatedReconcileRepairsReplica(t *testing.T) {
	a, b, c := newMem(), newMem(), newMem()
	c.failPut = true
	st, _ := NewReplicatedStore(members(a, b, c), 2)
	data := []byte("payload")
	d := digest.FromBytes(data)
	if err := st.Put(d, bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	c.failPut = false
	rec := st.(Reconciler)

	if changed, err := rec.Reconcile(d, true); err != nil || !changed {
		t.Fatalf("dry run = (%v, %v), want changed", changed, err)
	}
	if c.Exists(d) {
		t.Fatal("dry run must not copy")
	}
	if changed, err := rec.Reconcile(d, false); err != nil || !changed || !c.Exists(d) {
		t.Fatalf("reconcile = (%v, %v), exists=%v", changed, err, c.Exists(d))
	}
	if changed, _ := rec.Reconcile(d, false); changed {
		t.Error("second reconcile should find nothing to do")
	}
}

func TestShardedPlacementIsStableAndSpread(t *testing.T) {
	a, b, c := newMem(), newMem(), newMem()
	st, _ := NewShardedStore(members(a, b, c))
	counts := map[*memBlobs]int{}
	for i := 0; i < 300; i++ {
		data := []byte(fmt.Sprintf("blob-%d", i))
		d := digest.FromBytes(data)
		if err := st.Put(d, bytes.NewReader(data)); err != nil {
			t.Fatal(err)
		}
		holders := 0
		for _, m := range []*memBlobs{a, b, c} {
			if m.Exists(d) {
				holders++
				counts[m]++
			}
		}
		if holders != 1 {
			t.Fatalf("blob %d is on %d members, want exactly 1", i, holders)
		}
		if _, _, err := st.Get(d); err != nil {
			t.Fatalf("read back: %v", err)
		}
	}
	for m, n := range counts {
		if n < 50 {
			t.Errorf("member %p holds only %d of 300 blobs: distribution is lopsided", m, n)
		}
	}
}

func TestShardedAddingMemberMovesLittleAndStaysReadable(t *testing.T) {
	a, b := newMem(), newMem()
	st2, _ := NewShardedStore(members(a, b))
	var ds []digest.Digest
	for i := 0; i < 300; i++ {
		data := []byte(fmt.Sprintf("blob-%d", i))
		d := digest.FromBytes(data)
		ds = append(ds, d)
		_ = st2.Put(d, bytes.NewReader(data))
	}
	// A third member joins.
	c := newMem()
	st3, _ := NewShardedStore(members(a, b, c))
	moved := 0
	for _, d := range ds {
		if _, _, err := st3.Get(d); err != nil {
			t.Fatalf("blob unreachable after adding a member: %v", err)
		}
		if st3.(*shardedStore).order(d)[0].Name == "m2" {
			moved++
		}
	}
	if moved < 50 || moved > 150 {
		t.Errorf("%d of 300 blobs now owned by the new member, want about a third", moved)
	}
	// Reconcile moves them and leaves no strays.
	rec := st3.(Reconciler)
	for _, d := range ds {
		if _, err := rec.Reconcile(d, false); err != nil {
			t.Fatal(err)
		}
	}
	if len(c.data) != moved {
		t.Errorf("new member holds %d blobs, want %d", len(c.data), moved)
	}
	if len(a.data)+len(b.data)+len(c.data) != 300 {
		t.Error("blobs were duplicated or lost during rebalancing")
	}
	for _, d := range ds {
		if _, _, err := st3.Get(d); err != nil {
			t.Fatalf("unreachable after rebalance: %v", err)
		}
	}
}

func TestDeleteRemovesFromAllMembers(t *testing.T) {
	a, b := newMem(), newMem()
	st, _ := NewReplicatedStore(members(a, b), 2)
	data := []byte("z")
	d := digest.FromBytes(data)
	_ = st.Put(d, bytes.NewReader(data))
	if err := st.Delete(d); err != nil {
		t.Fatal(err)
	}
	if a.Exists(d) || b.Exists(d) {
		t.Error("blob survived delete on a member")
	}
	if err := st.Delete(d); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting an absent blob = %v, want ErrNotFound", err)
	}
}
