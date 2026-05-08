package lifecycle

import (
	"context"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
)

func TestRun_ContextCancelEndsCleanly(t *testing.T) {
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{Chain: chain.NewInMemory(sk.Address()), Signer: sk, Audit: nil, Clock: clock.System{}})
	s, err := grpc.NewServer(grpc.Config{Publisher: pub})
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunConfig{Server: s, Store: st, Logger: logger.Discard(), ShutdownTimeout: 100 * time.Millisecond})
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func TestRun_NilServerError(t *testing.T) {
	if err := Run(context.Background(), RunConfig{}); err == nil {
		t.Fatal("expected error on nil server")
	}
}
