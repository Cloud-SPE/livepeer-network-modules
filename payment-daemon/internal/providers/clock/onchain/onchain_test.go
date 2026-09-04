package onchain

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	ethcommon "github.com/ethereum/go-ethereum/common"
	ethtypes "github.com/ethereum/go-ethereum/core/types"

	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

var (
	roundsAddr  = ethcommon.HexToAddress("0x0000000000000000000000000000000000000010")
	bondingAddr = ethcommon.HexToAddress("0x0000000000000000000000000000000000000020")
)

// chainStub is a programmable RoundsManager + BondingManager behind a
// FakeRPC: it answers the three eth_calls the clock issues and the head
// header, and counts blockHashForRound calls so the per-round cache can
// be asserted.
type chainStub struct {
	rpc       *chaintesting.FakeRPC
	round     atomic.Int64
	pool      atomic.Int64
	head      atomic.Int64
	hashCalls atomic.Int32
}

func newChainStub(t *testing.T) *chainStub {
	t.Helper()
	s := &chainStub{rpc: chaintesting.NewFakeRPC()}
	s.round.Store(12345)
	s.pool.Store(100)
	s.head.Store(777)
	s.rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 4 {
			return nil, errors.New("short calldata")
		}
		sel := msg.Data[:4]
		switch {
		case bytes.Equal(sel, roundsABI.Methods["lastInitializedRound"].ID):
			if *msg.To != roundsAddr {
				return nil, errors.New("lastInitializedRound sent to wrong contract")
			}
			return roundsABI.Methods["lastInitializedRound"].Outputs.Pack(big.NewInt(s.round.Load()))
		case bytes.Equal(sel, roundsABI.Methods["blockHashForRound"].ID):
			s.hashCalls.Add(1)
			args, err := roundsABI.Methods["blockHashForRound"].Inputs.Unpack(msg.Data[4:])
			if err != nil {
				return nil, err
			}
			var h [32]byte
			h[31] = byte(args[0].(*big.Int).Int64()) // hash encodes the round it was asked for
			return roundsABI.Methods["blockHashForRound"].Outputs.Pack(h)
		case bytes.Equal(sel, bondingABI.Methods["getTranscoderPoolSize"].ID):
			if *msg.To != bondingAddr {
				return nil, errors.New("getTranscoderPoolSize sent to wrong contract")
			}
			return bondingABI.Methods["getTranscoderPoolSize"].Outputs.Pack(big.NewInt(s.pool.Load()))
		}
		return nil, errors.New("unstubbed selector")
	}
	s.rpc.HeaderByNumberFunc = func(_ context.Context, n *big.Int) (*ethtypes.Header, error) {
		if n != nil {
			return nil, errors.New("clock must ask for the head, not a specific block")
		}
		return &ethtypes.Header{Number: big.NewInt(s.head.Load())}, nil
	}
	return s
}

func newClock(t *testing.T, s *chainStub, interval time.Duration) *Clock {
	t.Helper()
	c, err := New(context.Background(), Config{
		RoundsManager:   roundsAddr,
		BondingManager:  bondingAddr,
		RefreshInterval: interval,
	}, s.rpc)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestClock_InitialSync(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)

	if got := c.LastInitializedRound(); got != 12345 {
		t.Errorf("LastInitializedRound = %d; want 12345", got)
	}
	hash := c.LastInitializedL1BlockHash()
	if len(hash) != 32 || hash[31] != byte(12345%256) {
		t.Errorf("LastInitializedL1BlockHash = %x; want 32 bytes tagged with the round", hash)
	}
	if got := c.LastSeenL1Block(); got.Int64() != 777 {
		t.Errorf("LastSeenL1Block = %s; want 777", got)
	}
	if got := c.GetTranscoderPoolSize(); got.Int64() != 100 {
		t.Errorf("GetTranscoderPoolSize = %s; want 100", got)
	}
	// Returned slices and ints are copies: mutating them must not touch
	// the clock's state.
	hash[0] = 0xff
	if c.LastInitializedL1BlockHash()[0] == 0xff {
		t.Error("LastInitializedL1BlockHash returned an aliased slice")
	}
}

func TestClock_BlockHashCachedPerRound(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	if got := s.hashCalls.Load(); got != 1 {
		t.Fatalf("blockHashForRound calls after initial sync = %d; want 1", got)
	}
	// Same round: cache hit, no new call.
	if err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.hashCalls.Load(); got != 1 {
		t.Errorf("blockHashForRound calls after same-round refresh = %d; want 1", got)
	}
	// Round advances: exactly one more call, and the hash follows it.
	s.round.Store(12346)
	if err := c.refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.hashCalls.Load(); got != 2 {
		t.Errorf("blockHashForRound calls after round advance = %d; want 2", got)
	}
	if c.LastInitializedRound() != 12346 || c.LastInitializedL1BlockHash()[31] != byte(12346%256) {
		t.Errorf("state did not follow the round: round=%d hash=%x", c.LastInitializedRound(), c.LastInitializedL1BlockHash())
	}
}

// A refresh that fails on the RPC leaves the last-good state in place
// and the next refresh recovers: the same shape as an endpoint failing
// over mid-poll.
func TestClock_RefreshFailureKeepsLastGoodThenRecovers(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)

	s.round.Store(20000)
	s.head.Store(800)
	s.rpc.InjectError("CallContract", errors.New("connection refused"))
	err := c.refresh(context.Background())
	if err == nil || !strings.Contains(err.Error(), "lastInitializedRound") {
		t.Fatalf("expected the failing read to be named, got %v", err)
	}
	if c.LastInitializedRound() != 12345 || c.LastSeenL1Block().Int64() != 777 {
		t.Fatalf("failed refresh must not touch state: round=%d head=%s", c.LastInitializedRound(), c.LastSeenL1Block())
	}

	if err := c.refresh(context.Background()); err != nil {
		t.Fatalf("recovery refresh: %v", err)
	}
	if c.LastInitializedRound() != 20000 || c.LastSeenL1Block().Int64() != 800 {
		t.Fatalf("state after recovery: round=%d head=%s", c.LastInitializedRound(), c.LastSeenL1Block())
	}
}

func TestClock_HeadHeaderFailureIsNamed(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	s.rpc.InjectError("HeaderByNumber", errors.New("boom"))
	if err := c.refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "head header") {
		t.Fatalf("err = %v", err)
	}
	s.rpc.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) { return &ethtypes.Header{}, nil }
	if err := c.refresh(context.Background()); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("nil head number: err = %v", err)
	}
}

func TestClock_StartRefreshesAndStopIsIdempotent(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	s.round.Store(50000)
	deadline := time.Now().Add(2 * time.Second)
	for c.LastInitializedRound() != 50000 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if c.LastInitializedRound() != 50000 {
		t.Fatal("refresh loop never observed the new round")
	}
	c.Stop()
	c.Stop()
}

func TestClock_ConstructionErrors(t *testing.T) {
	s := newChainStub(t)
	ctx := context.Background()
	if _, err := New(ctx, Config{RoundsManager: roundsAddr, BondingManager: bondingAddr}, nil); err == nil {
		t.Error("nil client must error")
	}
	if _, err := New(ctx, Config{BondingManager: bondingAddr}, s.rpc); err == nil {
		t.Error("empty RoundsManager must error")
	}
	if _, err := New(ctx, Config{RoundsManager: roundsAddr}, s.rpc); err == nil {
		t.Error("empty BondingManager must error")
	}
	s.rpc.InjectError("CallContract", errors.New("down"))
	if _, err := New(ctx, Config{RoundsManager: roundsAddr, BondingManager: bondingAddr}, s.rpc); err == nil || !strings.Contains(err.Error(), "initial sync") {
		t.Errorf("initial sync failure must abort construction, got %v", err)
	}
}

func TestClock_ZeroValuesBeforeSync(t *testing.T) {
	c := &Clock{}
	if c.LastInitializedRound() != 0 || c.LastInitializedL1BlockHash() != nil || c.LastSeenL1Block().Sign() != 0 || c.GetTranscoderPoolSize().Sign() != 0 {
		t.Fatal("unsynced clock must report zero values")
	}
}
