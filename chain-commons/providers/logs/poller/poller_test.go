package poller_test

import (
	"context"
	"errors"
	"math/big"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logs/poller"
	chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

func TestNew_RequiresRPCAndStore(t *testing.T) {
	if _, err := poller.New(poller.Options{}); err == nil {
		t.Errorf("New without RPC should fail")
	}
	if _, err := poller.New(poller.Options{RPC: chaintest.NewFakeRPC()}); err == nil {
		t.Errorf("New without Store should fail")
	}
}

func TestSubscribe_NewName_StartsAtHead(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(1000)}, nil
	}

	p, _ := poller.New(poller.Options{
		RPC:          rpc,
		Store:        chaintest.NewFakeStore(),
		PollInterval: 50 * time.Millisecond,
	})

	sub, err := p.Subscribe(context.Background(), "test", ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	last, err := p.LastConsumed("test")
	if err != nil {
		t.Fatalf("LastConsumed: %v", err)
	}
	if last != 0 {
		t.Errorf("LastConsumed for new name should be 0, got %d", last)
	}
}

func TestSubscribe_DuplicateName_Fails(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(0)}, nil
	}
	p, _ := poller.New(poller.Options{RPC: rpc, Store: chaintest.NewFakeStore()})
	sub, err := p.Subscribe(context.Background(), "dup", ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()
	_, err = p.Subscribe(context.Background(), "dup", ethereum.FilterQuery{})
	if !errors.Is(err, poller.ErrSubscriptionExists) {
		t.Errorf("second Subscribe = %v, want ErrSubscriptionExists", err)
	}
}

func TestAckPersistsOffset(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(0)}, nil
	}
	st := chaintest.NewFakeStore()
	p, _ := poller.New(poller.Options{RPC: rpc, Store: st})
	sub, _ := p.Subscribe(context.Background(), "ack", ethereum.FilterQuery{})
	defer sub.Close()

	if err := sub.Ack(chain.BlockNumber(42)); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	got, err := p.LastConsumed("ack")
	if err != nil {
		t.Fatalf("LastConsumed: %v", err)
	}
	if got != 42 {
		t.Errorf("LastConsumed = %d, want 42", got)
	}
}

func TestSubscribe_PollsLogs(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	var head atomic.Int64
	head.Store(100)
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(head.Load())}, nil
	}
	rpc.FilterLogsFunc = func(_ context.Context, q ethereum.FilterQuery) ([]types.Log, error) {
		// One synthetic log per call.
		return []types.Log{{
			Address:     common.HexToAddress("0x01"),
			BlockNumber: q.FromBlock.Uint64(),
		}}, nil
	}

	st := chaintest.NewFakeStore()
	p, _ := poller.New(poller.Options{
		RPC:          rpc,
		Store:        st,
		PollInterval: 20 * time.Millisecond,
		ChunkSize:    10,
	})

	// Pre-set offset so first poll returns logs.
	bucket, _ := st.Bucket("chain_commons_log_offsets")
	_ = bucket.Put([]byte("trial"), []byte{0, 0, 0, 0, 0, 0, 0, 50}) // last consumed = 50

	sub, err := p.Subscribe(context.Background(), "trial", ethereum.FilterQuery{})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	defer sub.Close()

	deadline := time.After(2 * time.Second)
	select {
	case logs := <-sub.Events():
		if len(logs) == 0 {
			t.Errorf("expected at least one log")
		}
	case <-deadline:
		t.Fatal("did not receive any logs")
	}
}

func TestSubscribe_ResumeFromPersistedOffset(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(200)}, nil
	}

	st := chaintest.NewFakeStore()
	bucket, _ := st.Bucket("chain_commons_log_offsets")
	_ = bucket.Put([]byte("resume"), []byte{0, 0, 0, 0, 0, 0, 0, 100}) // last consumed 100

	p, _ := poller.New(poller.Options{RPC: rpc, Store: st})
	sub, _ := p.Subscribe(context.Background(), "resume", ethereum.FilterQuery{})
	defer sub.Close()

	last, _ := p.LastConsumed("resume")
	if last != 100 {
		t.Errorf("LastConsumed = %d, want 100", last)
	}
}

func TestSubscribe_HeadFetchFailsOnNew(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.InjectErrorN("HeaderByNumber", errors.New("rpc dead"), 100)
	p, _ := poller.New(poller.Options{RPC: rpc, Store: chaintest.NewFakeStore()})

	_, err := p.Subscribe(context.Background(), "fresh", ethereum.FilterQuery{})
	if err == nil {
		t.Fatalf("expected error when head fetch fails for fresh subscription")
	}
	c := cerrors.Classify(err)
	if c.Class != cerrors.ClassTransient {
		t.Errorf("err class = %v, want ClassTransient", c.Class)
	}
}

func TestUnsubscribe_AllowsResubscribe(t *testing.T) {
	rpc := chaintest.NewFakeRPC()
	rpc.HeaderByNumberFunc = func(_ context.Context, _ *big.Int) (*types.Header, error) {
		return &types.Header{Number: big.NewInt(0)}, nil
	}
	p, _ := poller.New(poller.Options{RPC: rpc, Store: chaintest.NewFakeStore()})

	if _, err := p.Subscribe(context.Background(), "x", ethereum.FilterQuery{}); err != nil {
		t.Fatalf("Subscribe 1: %v", err)
	}
	if err := p.Unsubscribe("x"); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	if _, err := p.Subscribe(context.Background(), "x", ethereum.FilterQuery{}); err != nil {
		t.Errorf("Re-subscribe after Unsubscribe should work: %v", err)
	}
	_ = p.Unsubscribe("x")
}
