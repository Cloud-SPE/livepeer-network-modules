package audit

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func TestAppendQuery_RoundTrip(t *testing.T) {
	r := New(store.NewMemory())
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")

	e1 := types.AuditEvent{
		At:         time.Unix(1745000000, 0).UTC(),
		EthAddress: addr,
		Kind:       types.AuditManifestFetched,
		Mode:       types.ModeWellKnown,
	}
	e2 := types.AuditEvent{
		At:         time.Unix(1745000060, 0).UTC(),
		EthAddress: addr,
		Kind:       types.AuditSignatureInvalid,
	}
	if err := r.Append(e1); err != nil {
		t.Fatal(err)
	}
	if err := r.Append(e2); err != nil {
		t.Fatal(err)
	}

	got, err := r.Query(addr, time.Time{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if !got[0].At.Before(got[1].At) {
		t.Fatalf("expected time-ascending order")
	}
}

func TestQuery_FilterSinceAndLimit(t *testing.T) {
	r := New(store.NewMemory())
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")

	for i := 0; i < 5; i++ {
		_ = r.Append(types.AuditEvent{
			At:         time.Unix(int64(1745000000+i), 0).UTC(),
			EthAddress: addr,
			Kind:       types.AuditManifestFetched,
		})
	}
	got, err := r.Query(addr, time.Unix(1745000003, 0), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("since: got %d, want 2", len(got))
	}
	got, _ = r.Query(addr, time.Time{}, 2)
	if len(got) != 2 {
		t.Fatalf("limit: got %d, want 2", len(got))
	}
}

func TestAppend_RejectsEmptyAddress(t *testing.T) {
	r := New(store.NewMemory())
	if err := r.Append(types.AuditEvent{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestQuery_AddressIsolation(t *testing.T) {
	r := New(store.NewMemory())
	a, _ := types.ParseEthAddress("0xaaaaaa0000000000000000000000000000000000")
	b, _ := types.ParseEthAddress("0xbbbbbb0000000000000000000000000000000000")
	_ = r.Append(types.AuditEvent{EthAddress: a, Kind: types.AuditManifestFetched})
	_ = r.Append(types.AuditEvent{EthAddress: b, Kind: types.AuditManifestFetched})

	got, _ := r.Query(a, time.Time{}, 0)
	if len(got) != 1 {
		t.Fatalf("expected 1 event for a, got %d", len(got))
	}
}
