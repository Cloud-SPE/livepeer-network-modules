package manifestcache

import (
	"bytes"
	"encoding/gob"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func signedEntry(seq uint64, hash byte) *Entry {
	return &Entry{EthAddress: types.EthAddress("0xabcdef0000000000000000000000000000000000"), Mode: types.ModeWellKnown, PublicationSeq: seq, Manifest: &types.Manifest{CanonicalSHA256: [32]byte{hash}}}
}
func TestReplaySurvivesDeleteAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	kv, err := store.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	r := New(kv)
	if err := r.Put(signedEntry(5, 5)); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(signedEntry(5, 5).EthAddress); err != nil {
		t.Fatal(err)
	}
	if err := kv.Close(); err != nil {
		t.Fatal(err)
	}
	kv, err = store.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer kv.Close()
	r = New(kv)
	for _, e := range []*Entry{signedEntry(4, 4), signedEntry(5, 6)} {
		if err := r.Put(e); !errors.Is(err, types.ErrParse) {
			t.Fatalf("replay: %v", err)
		}
	}
	if err := r.Put(signedEntry(5, 5)); err != nil {
		t.Fatal(err)
	}
	if err := r.Put(signedEntry(6, 6)); err != nil {
		t.Fatal(err)
	}
}
func TestConcurrentPublicationsKeepHighest(t *testing.T) {
	r := New(store.NewMemory())
	var wg sync.WaitGroup
	for i := uint64(1); i <= 30; i++ {
		wg.Add(1)
		go func(seq uint64) {
			defer wg.Done()
			err := r.Put(signedEntry(seq, byte(seq)))
			if err != nil && !errors.Is(err, types.ErrParse) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	got, ok, err := r.Get(signedEntry(1, 1).EthAddress)
	if err != nil || !ok || got.PublicationSeq != 30 {
		t.Fatalf("got %+v %v", got, err)
	}
}

type failingWatermark struct{ store.Store }

func (f failingWatermark) Put(bucket, key, value []byte) error {
	if string(bucket) == string(watermarkBucket) {
		return errors.New("disk failure")
	}
	return f.Store.Put(bucket, key, value)
}
func TestWatermarkFailureDoesNotCache(t *testing.T) {
	r := New(failingWatermark{store.NewMemory()})
	e := signedEntry(1, 1)
	if err := r.Put(e); err == nil {
		t.Fatal("expected failure")
	}
	if _, ok, _ := r.Get(e.EthAddress); ok {
		t.Fatal("cached without replay protection")
	}
}

func TestLegacyWatermarkMigration(t *testing.T) {
	kv := store.NewMemory()
	r := New(kv)
	old := signedEntry(4, 0)
	old.ManifestSHA256 = [32]byte{7}
	// Simulate the previous cache schema, with no watermark bucket.
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(old); err != nil {
		t.Fatal(err)
	}
	if err := kv.Put(Bucket, key(old.EthAddress), buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	changed := signedEntry(4, 8)
	changed.ManifestSHA256 = [32]byte{8}
	if err := r.Put(changed); !errors.Is(err, types.ErrParse) {
		t.Fatalf("changed old payload accepted: %v", err)
	}
	same := signedEntry(4, 8)
	same.ManifestSHA256 = old.ManifestSHA256
	if err := r.Put(same); err != nil {
		t.Fatal(err)
	}
	same.ManifestSHA256 = [32]byte{9}
	if err := r.Put(same); err != nil {
		t.Fatalf("canonical hash not adopted: %v", err)
	}
}

type failingCacheWrite struct{ store.Store }

func (f failingCacheWrite) Put(bucket, key, value []byte) error {
	if string(bucket) == string(Bucket) {
		return errors.New("cache disk failure")
	}
	return f.Store.Put(bucket, key, value)
}
func TestWatermarkBeforeFailedCacheWrite(t *testing.T) {
	kv := store.NewMemory()
	r := New(kv)
	if err := r.Put(signedEntry(3, 3)); err != nil {
		t.Fatal(err)
	}
	broken := New(failingCacheWrite{kv})
	if err := broken.Put(signedEntry(4, 4)); err == nil {
		t.Fatal("expected failure")
	}
	// After restart the old cache cannot be served, even though its write survived.
	restarted := New(kv)
	if _, _, err := restarted.Get(signedEntry(3, 3).EthAddress); !errors.Is(err, types.ErrParse) {
		t.Fatalf("old cache remained usable: %v", err)
	}
	if err := restarted.Put(signedEntry(4, 4)); err != nil {
		t.Fatal(err)
	}
}
