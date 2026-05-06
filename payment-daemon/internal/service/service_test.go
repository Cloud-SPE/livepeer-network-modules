package service_test

import (
	"context"
	"net"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/proto/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/service"
	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// stand sets up a unix-socket gRPC server backed by a fresh BoltDB file in
// t.TempDir() and returns a connected client + cleanup.
func stand(t *testing.T) (pb.PayeeDaemonClient, *store.Store, func()) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "sessions.db")
	sockPath := filepath.Join(dir, "p.sock")

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	svc := service.New(st, nil)

	lis, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	gs := grpc.NewServer()
	pb.RegisterPayeeDaemonServer(gs, svc)
	go func() { _ = gs.Serve(lis) }()

	conn, err := grpc.NewClient(
		"unix://"+sockPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}

	cleanup := func() {
		_ = conn.Close()
		gs.GracefulStop()
		_ = st.Close()
	}
	return pb.NewPayeeDaemonClient(conn), st, cleanup
}

func TestHappyPath(t *testing.T) {
	client, st, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := client.Health(ctx, &pb.HealthRequest{}); err != nil {
		t.Fatalf("Health: %v", err)
	}

	open, err := client.OpenSession(ctx, &pb.OpenSessionRequest{
		Payment: &pb.Payment{
			CapabilityId:     "cap-1",
			OfferingId:       "off-1",
			ExpectedMaxUnits: 42,
			Ticket:           []byte("opaque"),
		},
		CapabilityId: "cap-1",
		OfferingId:   "off-1",
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if open.GetSessionId() == "" {
		t.Fatal("OpenSession: empty session_id")
	}

	if _, err := client.Debit(ctx, &pb.DebitRequest{SessionId: open.SessionId, Units: 42}); err != nil {
		t.Fatalf("Debit: %v", err)
	}
	if _, err := client.Reconcile(ctx, &pb.ReconcileRequest{SessionId: open.SessionId, ActualUnits: 30}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := client.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: open.SessionId}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}

	sess, err := st.Get(open.SessionId)
	if err != nil {
		t.Fatalf("store.Get: %v", err)
	}
	if !sess.Closed {
		t.Errorf("session not closed in store")
	}
	if got := len(sess.Debits); got != 1 || sess.Debits[0] != 42 {
		t.Errorf("debits = %v; want [42]", sess.Debits)
	}
	if sess.ActualUnits == nil || *sess.ActualUnits != 30 {
		t.Errorf("actualUnits = %v; want 30", sess.ActualUnits)
	}
}

func TestRejects(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := []struct {
		name string
		req  *pb.OpenSessionRequest
		want codes.Code
	}{
		{
			name: "empty payment",
			req:  &pb.OpenSessionRequest{},
			want: codes.InvalidArgument,
		},
		{
			name: "empty capability_id",
			req: &pb.OpenSessionRequest{Payment: &pb.Payment{
				OfferingId: "off-1", ExpectedMaxUnits: 1, Ticket: []byte("x"),
			}},
			want: codes.InvalidArgument,
		},
		{
			name: "empty offering_id",
			req: &pb.OpenSessionRequest{Payment: &pb.Payment{
				CapabilityId: "cap-1", ExpectedMaxUnits: 1, Ticket: []byte("x"),
			}},
			want: codes.InvalidArgument,
		},
		{
			name: "zero expected_max_units",
			req: &pb.OpenSessionRequest{Payment: &pb.Payment{
				CapabilityId: "cap-1", OfferingId: "off-1", Ticket: []byte("x"),
			}},
			want: codes.InvalidArgument,
		},
		{
			name: "empty ticket",
			req: &pb.OpenSessionRequest{Payment: &pb.Payment{
				CapabilityId: "cap-1", OfferingId: "off-1", ExpectedMaxUnits: 1,
			}},
			want: codes.InvalidArgument,
		},
		{
			name: "capability_id mismatch",
			req: &pb.OpenSessionRequest{
				Payment: &pb.Payment{
					CapabilityId: "cap-1", OfferingId: "off-1",
					ExpectedMaxUnits: 1, Ticket: []byte("x"),
				},
				CapabilityId: "cap-different",
				OfferingId:   "off-1",
			},
			want: codes.InvalidArgument,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := client.OpenSession(ctx, tc.req)
			if err == nil {
				t.Fatalf("OpenSession: want error, got nil")
			}
			if got := status.Code(err); got != tc.want {
				t.Errorf("status code = %v; want %v (err=%v)", got, tc.want, err)
			}
		})
	}
}

func TestDebitOnUnknownSession(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := client.Debit(ctx, &pb.DebitRequest{SessionId: "nope", Units: 1})
	if err == nil {
		t.Fatal("Debit: want error")
	}
	if got := status.Code(err); got != codes.NotFound {
		t.Errorf("status code = %v; want NotFound (err=%v)", got, err)
	}
}

func TestDebitAfterClose(t *testing.T) {
	client, _, cleanup := stand(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	open, err := client.OpenSession(ctx, &pb.OpenSessionRequest{
		Payment: &pb.Payment{
			CapabilityId: "cap-1", OfferingId: "off-1",
			ExpectedMaxUnits: 5, Ticket: []byte("x"),
		},
	})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if _, err := client.CloseSession(ctx, &pb.CloseSessionRequest{SessionId: open.SessionId}); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	_, err = client.Debit(ctx, &pb.DebitRequest{SessionId: open.SessionId, Units: 1})
	if err == nil {
		t.Fatal("Debit after close: want error")
	}
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("status code = %v; want FailedPrecondition (err=%v)", got, err)
	}
}
