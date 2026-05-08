package poolhints

import (
	"testing"

	"github.com/ethereum/go-ethereum/common"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

func TestNewValidates(t *testing.T) {
	if _, err := New(nil); err == nil {
		t.Fatal("expected error on nil store")
	}
}

func TestPutGetMiss(t *testing.T) {
	c, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	_, ok, err := c.Get(100, addr)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected cache miss")
	}
}

func TestPutGetHit(t *testing.T) {
	c, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	prev := common.HexToAddress("0x00000000000000000000000000000000000000AA")
	next := common.HexToAddress("0x00000000000000000000000000000000000000BB")

	if err := c.Put(100, addr, types.PoolHints{Prev: prev, Next: next}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(100, addr)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if got.Prev != prev || got.Next != next {
		t.Fatalf("hint mismatch: got prev=%s next=%s, want prev=%s next=%s", got.Prev, got.Next, prev, next)
	}
}

func TestDelete(t *testing.T) {
	c, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	prev := common.HexToAddress("0x00000000000000000000000000000000000000AA")

	if err := c.Put(100, addr, types.PoolHints{Prev: prev}); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(100, addr); err != nil {
		t.Fatal(err)
	}
	_, ok, err := c.Get(100, addr)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("expected cache miss after delete")
	}
}

func TestPurgeBefore(t *testing.T) {
	c, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	for _, r := range []chain.RoundNumber{95, 100, 101, 105} {
		if err := c.Put(r, addr, types.PoolHints{}); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := c.PurgeBefore(101)
	if err != nil {
		t.Fatal(err)
	}
	if deleted == 0 {
		t.Fatal("expected at least one deletion")
	}
	// Hits remaining at round=101 and round=105.
	for _, r := range []chain.RoundNumber{101, 105} {
		if _, ok, err := c.Get(r, addr); err != nil || !ok {
			t.Fatalf("expected hit at round %d", r)
		}
	}
	// Misses at 95 and 100.
	for _, r := range []chain.RoundNumber{95, 100} {
		if _, ok, err := c.Get(r, addr); err != nil || ok {
			t.Fatalf("expected miss at round %d", r)
		}
	}
}

func TestCountForRound(t *testing.T) {
	c, err := New(store.Memory())
	if err != nil {
		t.Fatal(err)
	}
	addr1 := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	addr2 := common.HexToAddress("0x00000000000000000000000000000000000000A2")
	addr3 := common.HexToAddress("0x00000000000000000000000000000000000000A3")

	if err := c.Put(100, addr1, types.PoolHints{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(100, addr2, types.PoolHints{}); err != nil {
		t.Fatal(err)
	}
	if err := c.Put(101, addr3, types.PoolHints{}); err != nil {
		t.Fatal(err)
	}

	if got, err := c.CountForRound(100); err != nil || got != 2 {
		t.Fatalf("CountForRound(100) = %d, %v; want 2", got, err)
	}
	if got, err := c.CountForRound(101); err != nil || got != 1 {
		t.Fatalf("CountForRound(101) = %d, %v; want 1", got, err)
	}
	if got, err := c.CountForRound(999); err != nil || got != 0 {
		t.Fatalf("CountForRound(999) = %d, %v; want 0", got, err)
	}
}

func TestKeyRoundtrip(t *testing.T) {
	addr := common.HexToAddress("0x00000000000000000000000000000000000000A1")
	key := makeKey(42, addr)
	if len(key) != 28 {
		t.Fatalf("key len = %d; want 28", len(key))
	}
	round, gotAddr, ok := parseKey(key)
	if !ok {
		t.Fatal("parseKey returned !ok")
	}
	if round != 42 {
		t.Fatalf("round = %d; want 42", round)
	}
	if gotAddr != addr {
		t.Fatalf("addr = %s; want %s", gotAddr, addr)
	}
}

func TestParseKeyRejectsShort(t *testing.T) {
	if _, _, ok := parseKey([]byte{0x01}); ok {
		t.Fatal("expected !ok on short key")
	}
}

func TestDecodeHintsRejectsShort(t *testing.T) {
	if _, err := decodeHints([]byte{0x01}); err == nil {
		t.Fatal("expected error on short value")
	}
}

func TestEncodeHintsRoundtrip(t *testing.T) {
	h := types.PoolHints{
		Prev: common.HexToAddress("0x00000000000000000000000000000000000000AA"),
		Next: common.HexToAddress("0x00000000000000000000000000000000000000BB"),
	}
	bytes := encodeHints(h)
	got, err := decodeHints(bytes)
	if err != nil {
		t.Fatal(err)
	}
	if got != h {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, h)
	}
}
