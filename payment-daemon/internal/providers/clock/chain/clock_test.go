package chain

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
	"github.com/ethereum/go-ethereum/crypto"

	cchain "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/timesource"
	chaintesting "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/testing"
)

var (
	roundsAddr  = ethcommon.HexToAddress("0x0000000000000000000000000000000000000010")
	bondingAddr = ethcommon.HexToAddress("0x0000000000000000000000000000000000000020")
)

func sel(sig string) []byte { return crypto.Keccak256([]byte(sig))[:4] }

var (
	selCurrentRound            = sel("currentRound()")
	selRoundLength             = sel("roundLength()")
	selCurrentRoundStartBlock  = sel("currentRoundStartBlock()")
	selLastInitializedRound    = sel("lastInitializedRound()")
	selCurrentRoundInitialized = sel("currentRoundInitialized()")
	selBlockHashForRound       = sel("blockHashForRound(uint256)")
	selGetTranscoderPoolSize   = sel("getTranscoderPoolSize()")
)

func uintSlot(v uint64) []byte {
	out := make([]byte, 32)
	new(big.Int).SetUint64(v).FillBytes(out)
	return out
}

// chainStub is a programmable RoundsManager + BondingManager behind a
// FakeRPC: it answers every selector the timesource poller and the
// clock's bindings issue, and counts blockHashForRound calls so the
// per-round cache can be asserted.
type chainStub struct {
	rpc       *chaintesting.FakeRPC
	ctrl      *chaintesting.FakeController
	current   atomic.Int64
	lastInit  atomic.Int64
	pool      atomic.Int64
	head      atomic.Int64
	hashCalls atomic.Int32
	callFail  atomic.Bool
	hashFail  atomic.Bool
	poolFail  atomic.Bool
	headFail  atomic.Bool
}

func newChainStub(t *testing.T) *chainStub {
	t.Helper()
	s := &chainStub{
		rpc: chaintesting.NewFakeRPC(),
		ctrl: chaintesting.NewFakeController(controller.Addresses{
			RoundsManager:  roundsAddr,
			BondingManager: bondingAddr,
		}, nil),
	}
	s.current.Store(12345)
	s.lastInit.Store(12345)
	s.pool.Store(100)
	s.head.Store(777)
	s.rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if s.callFail.Load() {
			return nil, errors.New("connection refused")
		}
		if len(msg.Data) < 4 {
			return nil, errors.New("short calldata")
		}
		selector := msg.Data[:4]
		switch {
		case bytes.Equal(selector, selCurrentRound):
			return uintSlot(uint64(s.current.Load())), nil
		case bytes.Equal(selector, selRoundLength):
			return uintSlot(1), nil
		case bytes.Equal(selector, selCurrentRoundStartBlock):
			return uintSlot(0), nil
		case bytes.Equal(selector, selCurrentRoundInitialized):
			return uintSlot(1), nil
		case bytes.Equal(selector, selLastInitializedRound):
			if *msg.To != roundsAddr {
				return nil, errors.New("lastInitializedRound sent to wrong contract")
			}
			return uintSlot(uint64(s.lastInit.Load())), nil
		case bytes.Equal(selector, selBlockHashForRound):
			s.hashCalls.Add(1)
			if s.hashFail.Load() {
				return nil, errors.New("hash read failed")
			}
			round := new(big.Int).SetBytes(msg.Data[4:36]).Uint64()
			var h [32]byte
			h[31] = byte(round) // hash encodes the round it was asked for
			return h[:], nil
		case bytes.Equal(selector, selGetTranscoderPoolSize):
			if *msg.To != bondingAddr {
				return nil, errors.New("getTranscoderPoolSize sent to wrong contract")
			}
			if s.poolFail.Load() {
				return nil, errors.New("pool read failed")
			}
			return uintSlot(uint64(s.pool.Load())), nil
		}
		return nil, errors.New("unstubbed selector")
	}
	s.rpc.HeaderByNumberFunc = func(_ context.Context, n *big.Int) (*ethtypes.Header, error) {
		if s.headFail.Load() {
			return nil, errors.New("boom")
		}
		if n != nil {
			return nil, errors.New("clock must ask for the head, not a specific block")
		}
		return &ethtypes.Header{Number: big.NewInt(s.head.Load())}, nil
	}
	return s
}

func newClock(t *testing.T, s *chainStub, interval time.Duration) *Clock {
	t.Helper()
	c, err := New(context.Background(), Config{RefreshInterval: interval}, s.rpc, s.ctrl)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if !cond() {
		t.Fatalf("timed out waiting for %s", what)
	}
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
	c.GetTranscoderPoolSize().SetInt64(1)
	if c.GetTranscoderPoolSize().Int64() != 100 {
		t.Error("GetTranscoderPoolSize returned an aliased big.Int")
	}
}

func TestClock_BlockHashCachedPerRound(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	if got := s.hashCalls.Load(); got != 1 {
		t.Fatalf("blockHashForRound calls after initial sync = %d; want 1", got)
	}
	// Same round: cache hit, no new call.
	c.onRound(context.Background(), cchain.Round{Number: 12345, LastInitialized: 12345})
	if got := s.hashCalls.Load(); got != 1 {
		t.Errorf("blockHashForRound calls after same-round event = %d; want 1", got)
	}
	// Round advances: exactly one more call, and the hash follows it.
	c.onRound(context.Background(), cchain.Round{Number: 12346, LastInitialized: 12346})
	if got := s.hashCalls.Load(); got != 2 {
		t.Errorf("blockHashForRound calls after round advance = %d; want 2", got)
	}
	if c.LastInitializedRound() != 12346 || c.LastInitializedL1BlockHash()[31] != byte(12346%256) {
		t.Errorf("state did not follow the round: round=%d hash=%x", c.LastInitializedRound(), c.LastInitializedL1BlockHash())
	}
	// The head block survives a round event untouched.
	if c.LastSeenL1Block().Int64() != 777 {
		t.Errorf("round event clobbered the head block: %s", c.LastSeenL1Block())
	}
}

// A round event whose hash read fails leaves the last-good round in
// place; the next event recovers. The shape of an endpoint failing over
// mid-poll.
func TestClock_RoundRefreshFailureKeepsLastGoodThenRecovers(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)

	s.hashFail.Store(true)
	c.onRound(context.Background(), cchain.Round{Number: 20000, LastInitialized: 20000})
	if c.LastInitializedRound() != 12345 {
		t.Fatalf("failed refresh must not touch state: round=%d", c.LastInitializedRound())
	}
	s.hashFail.Store(false)
	c.onRound(context.Background(), cchain.Round{Number: 20000, LastInitialized: 20000})
	if c.LastInitializedRound() != 20000 || c.LastInitializedL1BlockHash()[31] != byte(20000%256) {
		t.Fatalf("state after recovery: round=%d hash=%x", c.LastInitializedRound(), c.LastInitializedL1BlockHash())
	}
}

// A block event records the head and re-reads the pool size. A pool
// read failure keeps the last-good pool size but still records the
// head: the two are independent facts.
func TestClock_BlockEventUpdatesHeadAndPool(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)

	s.pool.Store(150)
	c.onBlock(context.Background(), 800)
	if c.LastSeenL1Block().Int64() != 800 || c.GetTranscoderPoolSize().Int64() != 150 {
		t.Fatalf("after block event: head=%s pool=%s", c.LastSeenL1Block(), c.GetTranscoderPoolSize())
	}
	if c.LastInitializedRound() != 12345 {
		t.Fatalf("block event clobbered the round: %d", c.LastInitializedRound())
	}

	s.pool.Store(200)
	s.poolFail.Store(true)
	c.onBlock(context.Background(), 801)
	if c.LastSeenL1Block().Int64() != 801 || c.GetTranscoderPoolSize().Int64() != 150 {
		t.Fatalf("after failed pool read: head=%s pool=%s; want 801 / 150", c.LastSeenL1Block(), c.GetTranscoderPoolSize())
	}
}

// Start runs the real chain-commons poller: a round advance, a pool
// change and a new head all reach the Clock surface within a few polls.
func TestClock_StartFollowsPollerAndStopIsIdempotent(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, 5*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}

	s.current.Store(50000)
	s.lastInit.Store(50000)
	waitFor(t, "round advance", func() bool { return c.LastInitializedRound() == 50000 })
	if c.LastInitializedL1BlockHash()[31] != byte(50000%256) {
		t.Fatalf("hash did not follow the round: %x", c.LastInitializedL1BlockHash())
	}

	// lastInitializedRound moving without currentRound moving is also a
	// transition (initializeRound landing mid-round).
	s.lastInit.Store(50001)
	s.current.Store(50002)
	waitFor(t, "last-initialized advance", func() bool { return c.LastInitializedRound() == 50001 })

	s.pool.Store(42)
	s.head.Store(9000)
	waitFor(t, "pool + head", func() bool {
		return c.GetTranscoderPoolSize().Int64() == 42 && c.LastSeenL1Block().Int64() == 9000
	})

	c.Stop()
	c.Stop()
}

func TestClock_StartTimeSourceFailure(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	c.newTimeSource = func(context.Context) (timesource.TimeSource, error) {
		return nil, errors.New("no poller")
	}
	if err := c.Start(context.Background()); err == nil || !strings.Contains(err.Error(), "no poller") {
		t.Fatalf("Start err = %v", err)
	}
}

// scriptedSource is a TimeSource the test feeds by hand.
type scriptedSource struct {
	rounds chan cchain.Round
	blocks chan cchain.BlockNumber
	closed atomic.Bool
}

func (f *scriptedSource) CurrentRound(context.Context) (cchain.Round, error) {
	return cchain.Round{}, errors.New("unused")
}
func (f *scriptedSource) CurrentL1Block(context.Context) (cchain.BlockNumber, error) {
	return 0, errors.New("unused")
}
func (f *scriptedSource) SubscribeRounds(context.Context) (<-chan cchain.Round, error) {
	return f.rounds, nil
}
func (f *scriptedSource) SubscribeL1Blocks(context.Context) (<-chan cchain.BlockNumber, error) {
	return f.blocks, nil
}
func (f *scriptedSource) Close() error { f.closed.Store(true); return nil }

func TestClock_FollowerExitsWhenSourceCloses(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	src := &scriptedSource{rounds: make(chan cchain.Round, 1), blocks: make(chan cchain.BlockNumber, 1)}
	c.newTimeSource = func(context.Context) (timesource.TimeSource, error) { return src, nil }
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	src.rounds <- cchain.Round{Number: 99, LastInitialized: 98}
	waitFor(t, "scripted round", func() bool { return c.LastInitializedRound() == 98 })
	src.blocks <- 4321
	waitFor(t, "scripted block", func() bool { return c.LastSeenL1Block().Int64() == 4321 })

	close(src.rounds)
	c.wg.Wait() // follower exits on channel close without Stop
	c.Stop()
	if !src.closed.Load() {
		t.Fatal("Stop must close the timesource")
	}
}

func TestClock_FollowerExitsOnContextCancel(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, time.Hour)
	src := &scriptedSource{rounds: make(chan cchain.Round), blocks: make(chan cchain.BlockNumber)}
	c.newTimeSource = func(context.Context) (timesource.TimeSource, error) { return src, nil }
	ctx, cancel := context.WithCancel(context.Background())
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	c.wg.Wait()
	close(src.blocks)
	c.Stop()
}

func TestClock_ConstructionErrors(t *testing.T) {
	s := newChainStub(t)
	ctx := context.Background()
	cases := []struct {
		name string
		run  func() error
		want string
	}{
		{"nil rpc", func() error { _, err := New(ctx, Config{}, nil, s.ctrl); return err }, "nil rpc"},
		{"nil controller", func() error { _, err := New(ctx, Config{}, s.rpc, nil); return err }, "nil controller"},
		{"no RoundsManager", func() error {
			_, err := New(ctx, Config{}, s.rpc, chaintesting.NewFakeController(controller.Addresses{BondingManager: bondingAddr}, nil))
			return err
		}, "RoundsManager"},
		{"no BondingManager", func() error {
			_, err := New(ctx, Config{}, s.rpc, chaintesting.NewFakeController(controller.Addresses{RoundsManager: roundsAddr}, nil))
			return err
		}, "BondingManager"},
	}
	for _, tc := range cases {
		if err := tc.run(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v; want %q", tc.name, err, tc.want)
		}
	}

	s.callFail.Store(true)
	if _, err := New(ctx, Config{}, s.rpc, s.ctrl); err == nil || !strings.Contains(err.Error(), "lastInitializedRound") {
		t.Errorf("round read failure: %v", err)
	}
	s.callFail.Store(false)
	s.hashFail.Store(true)
	if _, err := New(ctx, Config{}, s.rpc, s.ctrl); err == nil || !strings.Contains(err.Error(), "blockHashForRound") {
		t.Errorf("hash read failure: %v", err)
	}
	s.hashFail.Store(false)
	s.poolFail.Store(true)
	if _, err := New(ctx, Config{}, s.rpc, s.ctrl); err == nil || !strings.Contains(err.Error(), "getTranscoderPoolSize") {
		t.Errorf("pool read failure: %v", err)
	}
	s.poolFail.Store(false)
	s.headFail.Store(true)
	if _, err := New(ctx, Config{}, s.rpc, s.ctrl); err == nil || !strings.Contains(err.Error(), "head header") {
		t.Errorf("head failure: %v", err)
	}
	s.headFail.Store(false)
	s.rpc.HeaderByNumberFunc = func(context.Context, *big.Int) (*ethtypes.Header, error) { return &ethtypes.Header{}, nil }
	if _, err := New(ctx, Config{}, s.rpc, s.ctrl); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Errorf("nil head number: %v", err)
	}
}

func TestClock_ZeroValuesBeforeSync(t *testing.T) {
	c := &Clock{}
	if c.LastInitializedRound() != 0 || c.LastInitializedL1BlockHash() != nil || c.LastSeenL1Block().Sign() != 0 || c.GetTranscoderPoolSize().Sign() != 0 {
		t.Fatal("unsynced clock must report zero values")
	}
}

func TestClock_DefaultInterval(t *testing.T) {
	s := newChainStub(t)
	c := newClock(t, s, 0)
	if c.cfg.RefreshInterval != 30*time.Second {
		t.Fatalf("default interval = %s; want 30s", c.cfg.RefreshInterval)
	}
}
