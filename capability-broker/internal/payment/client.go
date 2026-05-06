// Package payment defines the broker's interface to the receiver-side
// payment-daemon (`PayeeDaemon`) and provides Mock + GRPC
// implementations.
//
// The middleware uses Client without caring which is wired:
//   - GRPC: real client, talks to the daemon over a unix socket.
//   - Mock: in-process stub, used only for unit tests.
package payment

import (
	"context"
	"math/big"
)

// Client is the broker's PayeeDaemon adapter. The middleware drives a
// session lifecycle:
//
//	OpenSession → ProcessPayment → handler → DebitBalance → CloseSession
//
// Implementations may persist state (GRPC) or hold it in memory (Mock).
type Client interface {
	// OpenSession idempotently opens a payee-side session for the
	// given work_id with the supplied pricing metadata. Returns the
	// daemon's outcome (opened vs already-open).
	OpenSession(ctx context.Context, req OpenSessionRequest) (*OpenSessionResult, error)

	// ProcessPayment hands the daemon the raw `Payment` wire bytes
	// from the inbound `Livepeer-Payment` header (already
	// base64-decoded). The daemon validates and seals the session's
	// sender on the first call. Returns the sender (extracted from
	// the validated payment) plus the resulting balance.
	ProcessPayment(ctx context.Context, req ProcessPaymentRequest) (*ProcessPaymentResult, error)

	// DebitBalance is idempotent by (sender, work_id, debit_seq).
	// Retries with the same seq return the prior balance instead of
	// double-debiting.
	DebitBalance(ctx context.Context, req DebitBalanceRequest) (*big.Int, error)

	// CloseSession finalizes a session. After this call no further
	// ProcessPayment or DebitBalance against (sender, work_id) is
	// accepted.
	CloseSession(ctx context.Context, sender []byte, workID string) error
}

// OpenSessionRequest carries the (capability, offering, price,
// work_unit) tuple the daemon binds to the work_id.
type OpenSessionRequest struct {
	WorkID              string
	Capability          string
	Offering            string
	PricePerWorkUnitWei *big.Int
	WorkUnit            string
}

// OpenSessionResult is the daemon's outcome enum, simplified to a bool.
type OpenSessionResult struct {
	AlreadyOpen bool
}

// ProcessPaymentRequest is the inbound payment for a session.
type ProcessPaymentRequest struct {
	WorkID       string
	PaymentBytes []byte // base64-decoded Livepeer-Payment header value
}

// ProcessPaymentResult is what the daemon returns after sealing the
// sender.
type ProcessPaymentResult struct {
	Sender         []byte
	Balance        *big.Int
	WinnersQueued  int32
}

// DebitBalanceRequest captures one post-handler debit.
type DebitBalanceRequest struct {
	Sender    []byte
	WorkID    string
	WorkUnits int64
	DebitSeq  uint64
}
