package server

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/payment-daemon/internal/providers/metrics"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type payerStub struct {
	pb.UnimplementedPayerDaemonServer
}
type payeeStub struct {
	pb.UnimplementedPayeeDaemonServer
}
type payerAdminStub struct {
	pb.UnimplementedPayerAdminServer
}
type payeeAdminStub struct {
	pb.UnimplementedPayeeAdminServer
}

func TestServerListenServeAndStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payment.sock")
	s := NewSender(&payerStub{}, path, metrics.NewNoop(), slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err := s.Listen(); err != nil {
		t.Fatal(err)
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("second Listen: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat socket: %v", err)
	}
	if info.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode=%o", info.Mode().Perm())
	}
	done := make(chan error, 1)
	go func() { done <- s.Serve() }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, "unix://"+path, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	_ = conn.Close()
	s.GracefulStop()
	if err := <-done; !errors.Is(err, ErrStopped) {
		t.Fatalf("Serve returned %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket remains after stop: %v", err)
	}
}

func TestServerConstructorsAndListenFailure(t *testing.T) {
	sender := NewSenderWithAdmin(&payerStub{}, &payerAdminStub{}, SenderAdminConfig{Token: "x"}, filepath.Join(t.TempDir(), "sender.sock"), nil, nil)
	if _, ok := sender.grpcServer.GetServiceInfo()["livepeer.payments.v1.PayerAdmin"]; !ok {
		t.Fatal("PayerAdmin not registered")
	}
	sender.GracefulStop()
	receiver := NewReceiver(&payeeStub{}, &payeeAdminStub{}, ReceiverAdminConfig{Token: "x"}, filepath.Join(t.TempDir(), "receiver.sock"), nil, nil)
	if _, ok := receiver.grpcServer.GetServiceInfo()["livepeer.payments.v1.PayeeAdmin"]; !ok {
		t.Fatal("PayeeAdmin not registered")
	}
	receiver.GracefulStop()
	bad := NewSender(&payerStub{}, filepath.Join(t.TempDir(), "missing", "socket"), nil, nil)
	if err := bad.Listen(); err == nil {
		t.Fatal("Listen unexpectedly created a missing parent directory")
	}
	if err := bad.Serve(); err == nil {
		t.Fatal("Serve unexpectedly accepted an invalid socket path")
	}
}

func TestAdminAuthInterceptors(t *testing.T) {
	called := false
	handler := func(context.Context, any) (any, error) { called = true; return "ok", nil }
	nonAdmin := &grpc.UnaryServerInfo{FullMethod: "/livepeer.payments.v1.PayerDaemon/Health"}
	if got, err := senderAdminAuthInterceptor("")(context.Background(), nil, nonAdmin, handler); err != nil || got != "ok" || !called {
		t.Fatalf("non-admin got=%v err=%v called=%v", got, err, called)
	}
	admin := &grpc.UnaryServerInfo{FullMethod: "/livepeer.payments.v1.PayerAdmin/AdvanceRounds"}
	if _, err := senderAdminAuthInterceptor("")(context.Background(), nil, admin, handler); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unconfigured admin code=%v", status.Code(err))
	}
	if _, err := senderAdminAuthInterceptor("secret")(context.Background(), nil, admin, handler); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("missing bearer code=%v", status.Code(err))
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer secret"))
	if got, err := senderAdminAuthInterceptor("secret")(ctx, nil, admin, handler); err != nil || got != "ok" {
		t.Fatalf("valid sender admin got=%v err=%v", got, err)
	}
	payeeAdmin := &grpc.UnaryServerInfo{FullMethod: "/livepeer.payments.v1.PayeeAdmin/ListPendingRedemptions"}
	if got, err := receiverAdminAuthInterceptor("secret")(ctx, nil, payeeAdmin, handler); err != nil || got != "ok" {
		t.Fatalf("valid receiver admin got=%v err=%v", got, err)
	}
}

func TestMetricsInterceptorAndHelpers(t *testing.T) {
	if shortMethod("/a/b") != "b" || shortMethod("plain") != "plain" {
		t.Fatal("shortMethod mismatch")
	}
	table := newInFlightTable()
	if table.get("x") != table.get("x") || table.get("x") == table.get("y") {
		t.Fatal("in-flight counters are not stable per method")
	}
	handler := func(context.Context, any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/service/Method"}
	if got, err := metricsInterceptor(string(ModeSender), nil)(context.Background(), nil, info, handler); err != nil || got != "ok" {
		t.Fatalf("nil metrics got=%v err=%v", got, err)
	}
	if got, err := metricsInterceptor(metrics.RoleSender, metrics.NewNoop())(context.Background(), nil, info, handler); err != nil || got != "ok" {
		t.Fatalf("metrics got=%v err=%v", got, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _ = table.get("shared").Add(1) }()
	}
	wg.Wait()
	if table.get("shared").Load() != 8 {
		t.Fatalf("counter=%d", table.get("shared").Load())
	}
}
