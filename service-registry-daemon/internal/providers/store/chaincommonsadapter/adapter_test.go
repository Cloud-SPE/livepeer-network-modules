package chaincommonsadapter_test

import (
	"bytes"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	cstorebolt "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store/bolt"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store/chaincommonsadapter"
)

func TestNew_RequiresStore(t *testing.T) {
	if _, err := chaincommonsadapter.New(nil); err == nil {
		t.Errorf("New(nil) should fail")
	}
}

func TestPutGet_RoundTrip(t *testing.T) {
	a, err := chaincommonsadapter.New(chaintest.NewFakeStore())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer a.Close()

	if err := a.Put([]byte("manifestcache"), []byte("0xabc"), []byte("payload")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := a.Get([]byte("manifestcache"), []byte("0xabc"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("payload")) {
		t.Errorf("Get = %q, want payload", got)
	}
}

func TestGet_NotFoundMapsToRegistryErr(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeStore())
	defer a.Close()

	_, err := a.Get([]byte("any"), []byte("missing-key"))
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get(missing) = %v, want store.ErrNotFound (translated from chain-commons)", err)
	}
}

func TestDelete_Idempotent(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeStore())
	defer a.Close()

	_ = a.Put([]byte("b"), []byte("k"), []byte("v"))
	if err := a.Delete([]byte("b"), []byte("k")); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	// Second delete should not fail.
	if err := a.Delete([]byte("b"), []byte("k")); err != nil {
		t.Errorf("second Delete: %v", err)
	}
	if _, err := a.Get([]byte("b"), []byte("k")); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("Get after Delete = %v, want ErrNotFound", err)
	}
}

func TestForEach(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeStore())
	defer a.Close()

	bucket := []byte("audit")
	_ = a.Put(bucket, []byte("k1"), []byte("v1"))
	_ = a.Put(bucket, []byte("k2"), []byte("v2"))
	_ = a.Put(bucket, []byte("k3"), []byte("v3"))

	var keys []string
	err := a.ForEach(bucket, func(k, _ []byte) error {
		keys = append(keys, string(k))
		return nil
	})
	if err != nil {
		t.Fatalf("ForEach: %v", err)
	}
	if len(keys) != 3 {
		t.Errorf("ForEach saw %d keys, want 3", len(keys))
	}
}

func TestBucketIsolation(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeStore())
	defer a.Close()

	_ = a.Put([]byte("ba"), []byte("k"), []byte("av"))
	_ = a.Put([]byte("bb"), []byte("k"), []byte("bv"))

	gotA, _ := a.Get([]byte("ba"), []byte("k"))
	gotB, _ := a.Get([]byte("bb"), []byte("k"))
	if !bytes.Equal(gotA, []byte("av")) || !bytes.Equal(gotB, []byte("bv")) {
		t.Errorf("buckets should isolate: a=%q b=%q", gotA, gotB)
	}
}

func TestConcurrentBucketResolution(t *testing.T) {
	a, _ := chaincommonsadapter.New(chaintest.NewFakeStore())
	defer a.Close()

	// Race the bucket cache: many concurrent first-misses on the same name
	// should all converge on a single Bucket handle without error.
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := []byte{byte(i)}
			_ = a.Put([]byte("racey"), key, []byte("v"))
		}(i)
	}
	wg.Wait()

	count := 0
	_ = a.ForEach([]byte("racey"), func(_, _ []byte) error {
		count++
		return nil
	})
	if count != 32 {
		t.Errorf("expected 32 entries after concurrent put, got %d", count)
	}
}

// TestWithBoltBacking gives end-to-end coverage of the adapter against a
// real BoltDB-backed chain-commons.Store (not just the in-memory fake).
func TestWithBoltBacking(t *testing.T) {
	dir := t.TempDir()
	bs, err := cstorebolt.Open(filepath.Join(dir, "test.db"), cstorebolt.Default())
	if err != nil {
		t.Fatalf("bolt.Open: %v", err)
	}
	a, _ := chaincommonsadapter.New(bs)
	defer a.Close()

	if err := a.Put([]byte("manifestcache"), []byte("orch-1"), []byte("manifest-blob")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := a.Get([]byte("manifestcache"), []byte("orch-1"))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, []byte("manifest-blob")) {
		t.Errorf("Get returned %q", got)
	}
}
