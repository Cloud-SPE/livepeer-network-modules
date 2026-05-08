package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/logger"
	grpcrt "github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/protocol-daemon/internal/types"
)

// TestRun_WithListener boots the lifecycle WITH a real gRPC listener
// alongside the round-init service goroutine, asserts both run, and
// confirms ctx cancel cleanly stops everything (including socket cleanup).
func TestRun_WithListener(t *testing.T) {
	rc := newRoundClock(t)
	rs := newRoundInit(t)

	srv, err := grpcrt.New(grpcrt.Config{
		Mode:      types.ModeRoundInit,
		Version:   "test",
		ChainID:   42161,
		RoundInit: rs,
	})
	if err != nil {
		t.Fatalf("grpc.New: %v", err)
	}
	sock := filepath.Join(t.TempDir(), "protocol.sock")
	lis, err := grpcrt.NewListener(grpcrt.ListenerConfig{
		SocketPath: sock,
		Server:     srv,
		Logger:     logger.Slog(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}),
		Version:    "test",
	})
	if err != nil {
		t.Fatalf("NewListener: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{
			Mode:       types.ModeRoundInit,
			RoundInit:  rs,
			RoundClock: rc,
			Listener:   lis,
		})
	}()

	// Wait for the socket to bind so we know the listener goroutine
	// actually launched alongside the service goroutine.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("socket never appeared — listener goroutine didn't run")
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return within 3s of ctx cancel")
	}

	// Socket file removed on shutdown.
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket file not cleaned up: %v", err)
	}
}

// TestRun_NilListenerStillWorks ensures back-compat: existing tests that
// don't pass a Listener (the field is nil) keep booting cleanly.
func TestRun_NilListenerStillWorks(t *testing.T) {
	rc := newRoundClock(t)
	rs := newRoundInit(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(40 * time.Millisecond)
		cancel()
	}()
	if err := Run(ctx, Config{Mode: types.ModeRoundInit, RoundInit: rs, RoundClock: rc}); err != nil {
		t.Fatalf("Run with nil Listener: %v", err)
	}
}
