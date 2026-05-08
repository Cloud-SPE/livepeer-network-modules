package store

import (
	"errors"
	"path/filepath"
	"testing"
)

// runStoreContract is the shared contract test both Memory and Bolt
// must satisfy.
func runStoreContract(t *testing.T, s Store) {
	t.Helper()
	bk := []byte("test-bucket")
	if _, err := s.Get(bk, []byte("missing")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing key: expected ErrNotFound, got %v", err)
	}
	if err := s.Put(bk, []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	v, err := s.Get(bk, []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(v) != "v" {
		t.Fatalf("get: %s", v)
	}
	if err := s.Put(bk, []byte("k2"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	count := 0
	_ = s.ForEach(bk, func(k, v []byte) error {
		count++
		return nil
	})
	if count != 2 {
		t.Fatalf("foreach count: %d", count)
	}
	if err := s.Delete(bk, []byte("k")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(bk, []byte("k")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: expected ErrNotFound, got %v", err)
	}
}

func TestMemory_Contract(t *testing.T) {
	s := NewMemory()
	defer s.Close()
	runStoreContract(t, s)
}

func TestBolt_Contract(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenBolt(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runStoreContract(t, s)
}

func TestBolt_PersistsAcrossOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persist.db")
	s, err := OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put([]byte("b"), []byte("k"), []byte("v")); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()

	s2, err := OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	got, err := s2.Get([]byte("b"), []byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v" {
		t.Fatalf("persist: got %s", got)
	}
}

func TestMemory_DeleteEmptyBucket(t *testing.T) {
	s := NewMemory()
	if err := s.Delete([]byte("nope"), []byte("k")); err != nil {
		t.Fatalf("delete on empty bucket: %v", err)
	}
}

func TestBolt_DeleteEmptyBucket(t *testing.T) {
	s, err := OpenBolt(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Delete([]byte("nope"), []byte("k")); err != nil {
		t.Fatalf("delete on empty bucket: %v", err)
	}
}
