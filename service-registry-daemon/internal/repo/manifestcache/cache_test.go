package manifestcache

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func newRepo(t *testing.T) Repo {
	t.Helper()
	return New(store.NewMemory())
}

func TestRepo_PutGetRoundTrip(t *testing.T) {
	r := newRepo(t)
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	e := &Entry{
		EthAddress:    addr,
		ResolvedURI:   "https://orch.example.com:8935",
		Mode:          types.ModeWellKnown,
		FetchedAt:     time.Unix(1745000000, 0).UTC(),
		ChainSeenAt:   time.Unix(1745000000, 0).UTC(),
		SchemaVersion: types.SchemaVersion,
	}
	if err := r.Put(e); err != nil {
		t.Fatal(err)
	}
	got, ok, err := r.Get(addr)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ResolvedURI != e.ResolvedURI {
		t.Fatalf("URI mismatch: %s", got.ResolvedURI)
	}
	if got.Mode != types.ModeWellKnown {
		t.Fatalf("mode mismatch: %v", got.Mode)
	}
}

func TestRepo_GetMiss(t *testing.T) {
	r := newRepo(t)
	addr, _ := types.ParseEthAddress("0xabcdef0000000000000000000000000000000000")
	e, ok, err := r.Get(addr)
	if err != nil {
		t.Fatal(err)
	}
	if ok || e != nil {
		t.Fatalf("expected miss, got %+v", e)
	}
}

func TestRepo_DeleteThenList(t *testing.T) {
	r := newRepo(t)
	a, _ := types.ParseEthAddress("0xaaaaaa0000000000000000000000000000000000")
	b, _ := types.ParseEthAddress("0xbbbbbb0000000000000000000000000000000000")
	_ = r.Put(&Entry{EthAddress: a})
	_ = r.Put(&Entry{EthAddress: b})
	if err := r.Delete(a); err != nil {
		t.Fatal(err)
	}
	list, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != b {
		t.Fatalf("list after delete: %v", list)
	}
}

func TestRepo_PutNilOrEmpty(t *testing.T) {
	r := newRepo(t)
	if err := r.Put(nil); err == nil {
		t.Fatal("expected error for nil entry")
	}
	if err := r.Put(&Entry{}); err == nil {
		t.Fatal("expected error for empty eth_address")
	}
}
