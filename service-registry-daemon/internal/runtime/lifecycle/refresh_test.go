package lifecycle

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

type shutdownChain struct {
	started chan struct{}
	active  atomic.Int32
}

func (c *shutdownChain) GetServiceURI(ctx context.Context, _ types.EthAddress) (string, error) {
	c.active.Add(1)
	defer c.active.Add(-1)
	c.started <- struct{}{}
	<-ctx.Done()
	return "", ctx.Err()
}

type orderedStore struct {
	store.Store
	chain  *shutdownChain
	closed atomic.Bool
}

func (s *orderedStore) Close() error {
	if s.chain.active.Load() != 0 {
		return errors.New("store closed while refresh active")
	}
	s.closed.Store(true)
	return s.Store.Close()
}
func TestRunJoinsRefreshBeforeClosingStore(t *testing.T) {
	chain := &shutdownChain{started: make(chan struct{}, 1)}
	st := &orderedStore{Store: store.NewMemory(), chain: chain}
	cache := manifestcache.New(st)
	r := resolver.New(resolver.Config{Chain: chain, Cache: cache})
	r.Discover([]types.EthAddress{"0x1111111111111111111111111111111111111111"})
	server, err := grpc.NewServer(grpc.Config{Resolver: r, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, RunConfig{Server: server, Resolver: r, Store: st}) }()
	select {
	case <-chain.started:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not join workers")
	}
	if !st.closed.Load() {
		t.Fatal("store not closed")
	}
}
