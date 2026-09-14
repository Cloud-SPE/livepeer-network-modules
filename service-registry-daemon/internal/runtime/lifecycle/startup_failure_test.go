package lifecycle

import (
	"context"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/runtime/grpc"
	runtimeMetrics "github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/runtime/metrics"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/publisher"
	"net"
	"testing"
	"time"
)

func TestMetricsBindFailureDoesNotWaitForSecondExit(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	sk, err := signer.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	srv, err := grpc.NewServer(grpc.Config{Publisher: publisher.New(publisher.Config{Signer: sk})})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := runtimeMetrics.NewListener(runtimeMetrics.Config{Addr: occupied.Addr().String(), Recorder: metrics.NewNoop()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	if err := Run(ctx, RunConfig{Server: srv, MetricsListener: listener}); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("waited for an already-consumed exit notification")
	}
}
