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

type PaymentRejectionReason int32

const (
	PaymentRejectionReasonUnspecified          PaymentRejectionReason = 0
	PaymentRejectionReasonInvalidRecipientRand PaymentRejectionReason = 1
	PaymentRejectionReasonNonceReplay          PaymentRejectionReason = 2
	PaymentRejectionReasonNonceCapReached      PaymentRejectionReason = 3
	PaymentRejectionReasonInvalidSignature     PaymentRejectionReason = 4
	PaymentRejectionReasonOther                PaymentRejectionReason = 5
)

// Client is the broker's PayeeDaemon adapter. The middleware drives a
// session lifecycle:
//
//	OpenSession → ProcessPayment → handler → DebitBalance → CloseSession
//
// Plan 0015 grows the in-handler middle: long-running sessions
// periodically call DebitBalance with per-tick deltas plus
// SufficientBalance to confirm runway, terminating the handler when the
// runway disappears.
//
// Implementations may persist state (GRPC) or hold it in memory (Mock).
type Client interface {
	// GetTicketParams proxies the payee-side quote-free ticket-params
	// issuance surface. The broker exposes this over HTTP so sender-mode
	// payment daemons can mint tickets against payee-issued params.
	GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (*TicketParams, error)

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
	//
	// The result reports what the ledger actually CHARGED. Do not
	// recompute it: billing is cumulative, so a charge depends on the
	// running total and a recomputation from units alone disagrees
	// whenever a remainder carries — which is how a signed settlement
	// came to attest 5 wei for a debit that cost 4.
	DebitBalance(ctx context.Context, req DebitBalanceRequest) (*DebitResult, error)

	// SufficientBalance checks whether the (sender, work_id) balance
	// covers at least min_work_units of additional priced work, without
	// debiting. Plan 0015's interim-debit ticker calls this per tick;
	// false return triggers handler termination.
	SufficientBalance(ctx context.Context, req SufficientBalanceRequest) (*SufficientBalanceResult, error)

	// GetBalance returns the current balance for a (sender, work_id)
	// pair. Used for observability and conformance assertions.
	GetBalance(ctx context.Context, sender []byte, workID string) (*big.Int, error)

	// CloseSession finalizes a session. After this call no further
	// ProcessPayment or DebitBalance against (sender, work_id) is
	// accepted.
	CloseSession(ctx context.Context, sender []byte, workID string) error
}

// DebitResult is what a debit did, as reported by the ledger.
type DebitResult struct {
	Balance *big.Int
	// DebitedWei is the amount charged. Authoritative — this is the
	// number a settlement record must attest.
	DebitedWei *big.Int
	// CumulativeUnits is the running unit total after this debit, so a
	// settlement can state its position on the cumulative curve and stay
	// verifiable from the record alone.
	CumulativeUnits uint64
	// Replayed is true when the debit was deduplicated: nothing was
	// charged and no totals moved.
	Replayed bool
}

// OpenSessionRequest carries the (capability, offering, price,
// per_units, work_unit) tuple the daemon binds to the work_id.
type OpenSessionRequest struct {
	WorkID              string
	Capability          string
	Offering            string
	PricePerWorkUnitWei *big.Int
	// PerUnits is the denominator PricePerWorkUnitWei is quoted over.
	// The daemon bills ceil(units * price / per_units) cumulatively; a
	// zero here means 1, so an omitted denominator bills per_units times
	// the intended rate.
	PerUnits uint64
	WorkUnit string
}

// GetTicketParamsRequest mirrors the payee-daemon quote-free request
// without exposing generated proto types to the broker's HTTP layer.
type GetTicketParamsRequest struct {
	Sender     []byte
	Recipient  []byte
	FaceValue  *big.Int
	Capability string
	Offering   string
}

// TicketParams is the broker-local shape of payee-issued ticket params.
type TicketParams struct {
	Recipient         []byte
	FaceValue         *big.Int
	WinProb           *big.Int
	RecipientRandHash []byte
	Seed              []byte
	ExpirationBlock   *big.Int
	ExpirationParams  *TicketExpirationParams
	// HighestSeenNonce / HasSeenNonces are relayed verbatim from the
	// payee so a payer whose durable nonce counter was lost can resume
	// above what the payee has already recorded. The broker has no
	// opinion on either value; it is a pass-through.
	HighestSeenNonce uint32
	HasSeenNonces    bool
}

// TicketExpirationParams mirrors the payee-daemon response submessage.
type TicketExpirationParams struct {
	CreationRound          int64
	CreationRoundBlockHash []byte
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
	Sender            []byte
	CreditedEV        *big.Int
	Balance           *big.Int
	WinnersQueued     int32
	TicketStatus      []TicketStatus
	TicketsRejected   int32
	DominantRejection PaymentRejectionReason
}

type TicketStatus struct {
	SenderNonce     uint32
	RejectionReason PaymentRejectionReason
	CreditedEV      *big.Int
	WasWinning      bool
}

// DebitBalanceRequest captures one post-handler debit.
type DebitBalanceRequest struct {
	Sender    []byte
	WorkID    string
	WorkUnits int64
	DebitSeq  uint64
}

// SufficientBalanceRequest is the input to a runway check.
type SufficientBalanceRequest struct {
	Sender       []byte
	WorkID       string
	MinWorkUnits int64
}

// SufficientBalanceResult mirrors the PayeeDaemon proto response: a
// boolean answering "yes/no" plus the daemon's view of the current
// balance for diagnostics.
type SufficientBalanceResult struct {
	Sufficient bool
	Balance    *big.Int
}
