package lifecycle

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
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

// TestRun_WithListener boots the daemon WITH a real gRPC listener
// against a unix socket in a tmpdir, then cancels and verifies clean
// shutdown ordering: gRPC stop → store close.
func TestRun_WithListener(t *testing.T) {
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{
		Chain: chain.NewInMemory(sk.Address()), Signer: sk, Clock: clock.System{},
	})
	srv, err := grpc.NewServer(grpc.Config{Publisher: pub, Logger: logger.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "registry.sock")
	ln, err := grpc.NewListener(grpc.ListenerConfig{
		SocketPath: sock,
		Server:     srv,
		Logger:     logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}

	closed := &atomic.Bool{}
	st := &countingStore{closed: closed}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunConfig{
			Server: srv, Listener: ln, Store: st, Logger: logger.Discard(),
			ShutdownTimeout: 100 * time.Millisecond,
		})
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return")
	}
	if !closed.Load() {
		t.Fatal("Store.Close not called")
	}
}

func TestRun_StoreCloseError(t *testing.T) {
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{
		Chain: chain.NewInMemory(sk.Address()), Signer: sk, Clock: clock.System{},
	})
	srv, _ := grpc.NewServer(grpc.Config{Publisher: pub, Logger: logger.Discard()})

	st := &errorStore{err: errors.New("boom")}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, RunConfig{
			Server: srv, Store: st, Logger: logger.Discard(),
			ShutdownTimeout: 100 * time.Millisecond,
		})
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil || err.Error() != "boom" {
			t.Fatalf("expected store close error, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return")
	}
}

// countingStore just records that Close was called.
type countingStore struct {
	closed *atomic.Bool
}

func (s *countingStore) Get([]byte, []byte) ([]byte, error) { return nil, store.ErrNotFound }
func (s *countingStore) Put([]byte, []byte, []byte) error   { return nil }
func (s *countingStore) Delete([]byte, []byte) error        { return nil }
func (s *countingStore) ForEach([]byte, func(k, v []byte) error) error {
	return nil
}
func (s *countingStore) Close() error { s.closed.Store(true); return nil }

// errorStore returns an error from Close.
type errorStore struct {
	err error
}

func (s *errorStore) Get([]byte, []byte) ([]byte, error) { return nil, store.ErrNotFound }
func (s *errorStore) Put([]byte, []byte, []byte) error   { return nil }
func (s *errorStore) Delete([]byte, []byte) error        { return nil }
func (s *errorStore) ForEach([]byte, func(k, v []byte) error) error {
	return nil
}
func (s *errorStore) Close() error { return s.err }
