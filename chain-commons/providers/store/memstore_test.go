package store

import (
	"bytes"
	"testing"
)

func TestMemory_BasicCRUD(t *testing.T) {
	s := Memory()
	defer s.Close()

	b, err := s.Bucket("foo")
	if err != nil {
		t.Fatalf("Bucket: %v", err)
	}

	if err := b.Put([]byte("k1"), []byte("v1")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := b.Get([]byte("k1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("v1")) {
		t.Errorf("Get returned %q, want v1", got)
	}

	if err := b.Delete([]byte("k1")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := b.Get([]byte("k1")); err != ErrNotFound {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestMemory_BucketIsolation(t *testing.T) {
	s := Memory()
	defer s.Close()

	a, _ := s.Bucket("a")
	b, _ := s.Bucket("b")
	_ = a.Put([]byte("k"), []byte("av"))
	_ = b.Put([]byte("k"), []byte("bv"))

	gotA, _ := a.Get([]byte("k"))
	gotB, _ := b.Get([]byte("k"))
	if !bytes.Equal(gotA, []byte("av")) || !bytes.Equal(gotB, []byte("bv")) {
		t.Errorf("buckets should isolate keys: a=%q b=%q", gotA, gotB)
	}
}

func TestMemory_BucketsArePersistentAcrossReopen(t *testing.T) {
	s := Memory()
	a1, _ := s.Bucket("foo")
	_ = a1.Put([]byte("k"), []byte("v"))

	a2, _ := s.Bucket("foo")
	got, err := a2.Get([]byte("k"))
	if err != nil {
		t.Fatalf("Get from re-opened bucket: %v", err)
	}
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("re-opened bucket lost key: %q", got)
	}
}

func TestMemory_ForEach(t *testing.T) {
	s := Memory()
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("a"), []byte("1"))
	_ = b.Put([]byte("b"), []byte("2"))
	_ = b.Put([]byte("c"), []byte("3"))

	var keys []string
	_ = b.ForEach(func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if len(keys) != 3 {
		t.Errorf("ForEach saw %d keys, want 3", len(keys))
	}
	// ForEach is documented to iterate sorted-asc.
	if keys[0] != "a" || keys[1] != "b" || keys[2] != "c" {
		t.Errorf("ForEach order = %v, want a,b,c", keys)
	}
}

func TestMemory_Scan_Prefix(t *testing.T) {
	s := Memory()
	b, _ := s.Bucket("foo")
	_ = b.Put([]byte("redemption_pending_001"), []byte("a"))
	_ = b.Put([]byte("redemption_pending_002"), []byte("b"))
	_ = b.Put([]byte("redemption_redeemed_001"), []byte("c"))
	_ = b.Put([]byte("other"), []byte("d"))

	var keys []string
	_ = b.Scan([]byte("redemption_pending_"), func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if len(keys) != 2 {
		t.Errorf("Scan(prefix=redemption_pending_) saw %d keys, want 2", len(keys))
	}
}

func TestMemory_UpdateTransaction(t *testing.T) {
	s := Memory()
	err := s.Update(func(tx Tx) error {
		b, _ := tx.Bucket("a")
		_ = b.Put([]byte("k"), []byte("v"))
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	b, _ := s.Bucket("a")
	got, _ := b.Get([]byte("k"))
	if !bytes.Equal(got, []byte("v")) {
		t.Errorf("Update did not commit")
	}
}

func TestMemory_GetNotFound(t *testing.T) {
	s := Memory()
	b, _ := s.Bucket("foo")
	if _, err := b.Get([]byte("missing")); err != ErrNotFound {
		t.Errorf("Get(missing) = %v, want ErrNotFound", err)
	}
}

func TestMemory_GetCopiesValues(t *testing.T) {
	s := Memory()
	b, _ := s.Bucket("foo")
	v := []byte{1, 2, 3}
	_ = b.Put([]byte("k"), v)
	v[0] = 99 // mutate after Put
	got, _ := b.Get([]byte("k"))
	if got[0] == 99 {
		t.Errorf("Put should copy values to prevent caller mutation")
	}
}
