package audit

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestMetered_AppendBumpsCounter(t *testing.T) {
	rec := metrics.NewCounter()
	r := WithMetrics(New(store.NewMemory()), rec)
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	if err := r.Append(types.AuditEvent{EthAddress: addr, Kind: types.AuditManifestFetched}); err != nil {
		t.Fatal(err)
	}
	if rec.AuditEvents.Load() != 1 {
		t.Fatalf("AuditEvents = %d", rec.AuditEvents.Load())
	}
}

func TestMetered_QueryStillWorks(t *testing.T) {
	rec := metrics.NewCounter()
	r := WithMetrics(New(store.NewMemory()), rec)
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	_ = r.Append(types.AuditEvent{EthAddress: addr, Kind: types.AuditManifestFetched, At: time.Unix(1, 0).UTC()})
	got, err := r.Query(addr, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events", len(got))
	}
}

func TestMetered_NilRecorderReturnsInner(t *testing.T) {
	inner := New(store.NewMemory())
	if got := WithMetrics(inner, nil); got != inner {
		t.Fatal("nil recorder should return the inner repo unchanged")
	}
}
