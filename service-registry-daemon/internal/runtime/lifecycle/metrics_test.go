package lifecycle

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	pmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/grpc"
	rmetrics "github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/metrics"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
)

// TestRun_WithBothListeners boots the daemon with BOTH the gRPC
// listener and the metrics listener so the lifecycle's parallel-Serve
// + parallel-Stop path is exercised.
func TestRun_WithBothListeners(t *testing.T) {
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{
		Chain: chain.NewInMemory(sk.Address()), Signer: sk, Clock: clock.System{},
	})
	srv, err := grpc.NewServer(grpc.Config{Publisher: pub, Logger: logger.Discard()})
	if err != nil {
		t.Fatal(err)
	}

	rec := pmetrics.NewPrometheus(pmetrics.PrometheusConfig{})

	// gRPC listener
	gln, err := grpc.NewListener(grpc.ListenerConfig{
		SocketPath: t.TempDir() + "/registry.sock",
		Server:     srv,
		Logger:     logger.Discard(),
		Recorder:   rec,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Metrics listener — bind to :0 then close to find a free port.
	probe, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := probe.Addr().String()
	_ = probe.Close()
	mln, err := rmetrics.NewListener(rmetrics.Config{
		Addr:     addr,
		Recorder: rec,
		Logger:   logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}

	st := store.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunConfig{
			Server:          srv,
			Listener:        gln,
			MetricsListener: mln,
			Store:           st,
			Logger:          logger.Discard(),
			ShutdownTimeout: 100 * time.Millisecond,
		})
	}()

	// Let both listeners bind, then trigger shutdown.
	time.Sleep(150 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
}
