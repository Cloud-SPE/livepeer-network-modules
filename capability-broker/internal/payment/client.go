// Package payment defines the broker's interface to the co-located
// payment-daemon and provides a v0.1 mock implementation.
//
// The real gRPC client (over unix socket) lands in plan 0005. The mock
// exists so the broker can serve real traffic end-to-end without the chain
// dependency, and so tests can inspect the lifecycle calls.
package payment

import (
	"context"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// Client is the broker's payment-daemon adapter. Two implementations:
//   - GRPC (this package): real client, talks to the payment-daemon over a
//     unix socket.
//   - Mock (this package): in-process stub, used only for unit tests.
//
// The middleware uses Client without caring which implementation is wired.
type Client interface {
	OpenSession(ctx context.Context, req OpenSessionRequest) (*Session, error)
	Debit(ctx context.Context, sessionID string, units uint64) error
	Reconcile(ctx context.Context, sessionID string, actualUnits uint64) error
	Close(ctx context.Context, sessionID string) error
}

// OpenSessionRequest carries everything the daemon needs to decide whether
// to open a session.
//
// The middleware decodes the Livepeer-Payment header into DecodedPayment
// before calling OpenSession. PaymentBlob is the original base64 string
// retained for diagnostics; CapabilityID / OfferingID are sourced from the
// inbound HTTP headers (the daemon also re-checks them defensively).
type OpenSessionRequest struct {
	CapabilityID   string
	OfferingID     string
	PaymentBlob    string
	DecodedPayment *pb.Payment
}

// Session is the handle returned by OpenSession; pass it to Debit /
// Reconcile / Close.
type Session struct {
	ID string
}
