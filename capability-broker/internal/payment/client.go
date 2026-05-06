// Package payment defines the broker's interface to the co-located
// payment-daemon and provides a v0.1 mock implementation.
//
// The real gRPC client (over unix socket) lands in plan 0005. The mock
// exists so the broker can serve real traffic end-to-end without the chain
// dependency, and so tests can inspect the lifecycle calls.
package payment

import "context"

// Client is the broker's payment-daemon adapter. The Mock implementation in
// this package is wired by default in v0.1; the real gRPC client comes in
// plan 0005.
type Client interface {
	OpenSession(ctx context.Context, req OpenSessionRequest) (*Session, error)
	Debit(ctx context.Context, sessionID string, units uint64) error
	Reconcile(ctx context.Context, sessionID string, actualUnits uint64) error
	Close(ctx context.Context, sessionID string) error
}

// OpenSessionRequest carries the inbound (capability, offering) plus the raw
// Livepeer-Payment header value. The real client decodes the protobuf
// envelope and validates the ticket; the mock simply records the blob.
type OpenSessionRequest struct {
	CapabilityID string
	OfferingID   string
	PaymentBlob  string
}

// Session is the handle returned by OpenSession; pass it to Debit /
// Reconcile / Close.
type Session struct {
	ID string
}
