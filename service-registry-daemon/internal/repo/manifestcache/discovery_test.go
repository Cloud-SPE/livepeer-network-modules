package manifestcache

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func TestDiscoveryPersistsWithoutPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	db, err := store.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	addr := types.EthAddress("0x1111111111111111111111111111111111111111")
	r := WithMetrics(New(db), metrics.NewNoop())
	_, ok, err := r.GetDiscovery(addr)
	if err != nil || ok {
		t.Fatal(ok, err)
	}
	st := types.DiscoveryStatus{SourceURI: "https://example.com", Compatibility: types.CompatibilityUnknown, FailureReason: types.FailureTransport, ConsecutiveFailures: 4, NextRetryAt: time.Unix(100, 0).UTC(), Invalidated: true}
	if err := r.PutDiscovery(addr, st); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.OpenBolt(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r = WithMetrics(New(db), metrics.NewNoop())
	got, ok, err := r.GetDiscovery(addr)
	if err != nil || !ok || got != st {
		t.Fatal(got, ok, err)
	}
	candidates, err := r.ListDiscovery()
	if err != nil || len(candidates) != 1 {
		t.Fatal(candidates, err)
	}
	cached, err := r.List()
	if err != nil || len(cached) != 0 {
		t.Fatal(cached, err)
	}
	if err := db.Put(discoveryBucket, key(addr), []byte("corrupt")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.GetDiscovery(addr); err == nil {
		t.Fatal("corrupt retry state accepted")
	}
}
