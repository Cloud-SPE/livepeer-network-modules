package poller_test

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/controller"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/timesource/poller"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// fixtureRPC encodes responses for the selectors the poller calls:
// currentRound, roundLength, currentRoundStartBlock, lastInitializedRound,
// currentRoundInitialized. HeaderByNumber returns a synthetic block.
//
// Defaults: lastInitialized = round (currentRoundInitialized = true). Tests
// that need uninitialized-current-round behaviour can override the
// CallContractFunc directly.
func fixtureRPC(round, length, startBlock, l2Block uint64) *chaintest.FakeRPC {
	rpc := chaintest.NewFakeRPC()
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		if len(msg.Data) < 4 {
			return nil, errors.New("bad calldata")
		}
		sel := msg.Data[:4]
		switch {
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRound()"))[:4]):
			return poller.AbiEncodeUint(round), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("roundLength()"))[:4]):
			return poller.AbiEncodeUint(length), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundStartBlock()"))[:4]):
			return poller.AbiEncodeUint(startBlock), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("lastInitializedRound()"))[:4]):
			return poller.AbiEncodeUint(round), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]):
			return poller.AbiEncodeBool(true), nil
		}
		return nil, errors.New("unknown selector")
	}
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(int64(l2Block))}, nil
	}
	return rpc
}

func newController(t *testing.T) controller.Controller {
	t.Helper()
	return chaintest.NewFakeController(controller.Addresses{
		RoundsManager: chain.Address{0x99},
	}, time.Now)
}

func TestNew_RequiresRPCAndController(t *testing.T) {
	if _, err := poller.New(poller.Options{}); err == nil {
		t.Errorf("New without RPC should fail")
	}
	if _, err := poller.New(poller.Options{RPC: chaintest.NewFakeRPC()}); err == nil {
		t.Errorf("New without Controller should fail")
	}
}

func TestCurrentRound_FetchesAllFields(t *testing.T) {
	rpc := fixtureRPC(42, 6646, 1000, 1500)
	ts, err := poller.New(poller.Options{
		RPC:        rpc,
		Controller: newController(t),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer ts.(closeable).Close()

	round, err := ts.CurrentRound(context.Background())
	if err != nil {
		t.Fatalf("CurrentRound: %v", err)
	}
	if round.Number != 42 {
		t.Errorf("Number = %d, want 42", round.Number)
	}
	if round.Length != 6646 {
		t.Errorf("Length = %d, want 6646", round.Length)
	}
	if round.StartBlock != 1000 {
		t.Errorf("StartBlock = %d, want 1000", round.StartBlock)
	}
}

// TestCurrentRound_LastInitializedTrailsCurrent asserts the round semantic
// payment-daemon's Clock adapter depends on: when currentRoundInitialized
// is false, lastInitializedRound trails currentRound by one (or more)
// rounds. Consumers that need a round whose blockHashForRound() is
// non-zero (ticket creationRound) MUST read Round.LastInitialized.
func TestCurrentRound_LastInitializedTrailsCurrent(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		sel := msg.Data[:4]
		switch {
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRound()"))[:4]):
			return poller.AbiEncodeUint(101), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("roundLength()"))[:4]):
			return poller.AbiEncodeUint(6646), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundStartBlock()"))[:4]):
			return poller.AbiEncodeUint(1000), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("lastInitializedRound()"))[:4]):
			return poller.AbiEncodeUint(100), nil // round 101 not yet initialized
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]):
			return poller.AbiEncodeBool(false), nil
		}
		return nil, errors.New("unknown selector")
	}
	ts, _ := poller.New(poller.Options{RPC: rpc, Controller: newController(t)})
	defer ts.(closeable).Close()

	round, err := ts.CurrentRound(context.Background())
	if err != nil {
		t.Fatalf("CurrentRound: %v", err)
	}
	if round.Number != 101 {
		t.Errorf("Number = %d, want 101", round.Number)
	}
	if round.LastInitialized != 100 {
		t.Errorf("LastInitialized = %d, want 100", round.LastInitialized)
	}
	if round.Initialized {
		t.Errorf("Initialized = true, want false")
	}
}

func TestCurrentL1Block_ReadsHeader(t *testing.T) {
	rpc := fixtureRPC(1, 1, 1, 555)
	ts, _ := poller.New(poller.Options{
		RPC:        rpc,
		Controller: newController(t),
	})
	defer ts.(closeable).Close()

	bn, err := ts.CurrentL1Block(context.Background())
	if err != nil {
		t.Fatalf("CurrentL1Block: %v", err)
	}
	if bn != 555 {
		t.Errorf("CurrentL1Block = %d, want 555", bn)
	}
}

func TestCloseCancelsInFlightPoll(t *testing.T) {
	rpc := fixtureRPC(1, 1, 1, 555)
	blocked := make(chan struct{})
	rpc.HeaderByNumberFunc = func(ctx context.Context, _ *big.Int) (*types.Header, error) {
		close(blocked)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	ts, err := poller.New(poller.Options{
		RPC:        rpc,
		Controller: newController(t),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	<-blocked

	done := make(chan struct{})
	go func() {
		_ = ts.(closeable).Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close did not cancel in-flight poll")
	}
}

func TestCurrentRound_NoControllerAddress(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	ctrl := chaintest.NewFakeController(controller.Addresses{}, time.Now) // no RoundsManager
	ts, _ := poller.New(poller.Options{
		RPC:        rpc,
		Controller: ctrl,
	})
	defer ts.(closeable).Close()

	if _, err := ts.CurrentRound(context.Background()); err == nil {
		t.Errorf("CurrentRound should fail when RoundsManager is unset")
	}
}

func TestSubscribeRounds_SeedAndUpdate(t *testing.T) {
	rpc := fixtureRPC(0, 0, 0, 0)
	var num atomic.Uint64
	num.Store(1)
	rpc.CallContractFunc = func(_ context.Context, msg ethereum.CallMsg, _ *big.Int) ([]byte, error) {
		// Respond with current `num` for currentRound + lastInitializedRound;
		// constant for the others.
		sel := msg.Data[:4]
		switch {
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRound()"))[:4]):
			return poller.AbiEncodeUint(num.Load()), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("roundLength()"))[:4]):
			return poller.AbiEncodeUint(6646), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundStartBlock()"))[:4]):
			return poller.AbiEncodeUint(1), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("lastInitializedRound()"))[:4]):
			return poller.AbiEncodeUint(num.Load()), nil
		case bytes.Equal(sel, crypto.Keccak256([]byte("currentRoundInitialized()"))[:4]):
			return poller.AbiEncodeBool(true), nil
		}
		return nil, errors.New("unknown selector")
	}

	ts, _ := poller.New(poller.Options{
		RPC:          rpc,
		Controller:   newController(t),
		PollInterval: 20 * time.Millisecond,
	})
	defer ts.(closeable).Close()

	sub, _ := ts.SubscribeRounds(context.Background())

	// Wait until we see the first round.
	deadline := time.After(2 * time.Second)
loop1:
	for {
		select {
		case r := <-sub:
			if r.Number == 1 {
				break loop1
			}
		case <-deadline:
			t.Fatalf("did not receive initial round")
		}
	}

	// Bump round; we should see the new round.
	num.Store(2)
	deadline = time.After(2 * time.Second)
	for {
		select {
		case r := <-sub:
			if r.Number == 2 {
				return // success
			}
		case <-deadline:
			t.Fatalf("did not receive bumped round")
		}
	}
}

type closeable interface {
	Close() error
}
