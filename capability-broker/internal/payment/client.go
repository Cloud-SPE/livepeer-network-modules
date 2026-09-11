// Package payment defines the broker's interface to the receiver-side
// payment-daemon (`PayeeDaemon`) and provides Mock + GRPC
// implementations.
//
// The broker uses Client for ticket-funding operations without caring which
// implementation is wired:
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

// Client is the broker's ticket-funding adapter. These operations establish
// recipient-random generations used to credit the wholesale account; they do
// not authorize or settle workload execution. Paid workloads require
// AccountClient.
type Client interface {
	// GetTicketParams proxies the payee-side quote-free ticket-params
	// issuance surface. The broker exposes this over HTTP so sender-mode
	// payment daemons can mint tickets against payee-issued params.
	GetTicketParams(ctx context.Context, req GetTicketParamsRequest) (*TicketParams, error)

	// OpenSession idempotently opens a payee-side session for the
	// given work_id with the supplied pricing metadata. Returns the
	// daemon's outcome (opened vs already-open).
	OpenSession(ctx context.Context, req OpenSessionRequest) (*OpenSessionResult, error)
}

// AccountClient is the mandatory paid-workload interface. Keeping it separate
// lets the broker detect an old daemon and fail closed rather than treating a
// funding ticket as workload authority.
type AccountClient interface {
	FundWholesaleAccount(ctx context.Context, paymentBytes []byte) (*FundWholesaleAccountResult, error)
	AdmitAuthorization(ctx context.Context, req AdmitAuthorizationRequest) (*AdmitAuthorizationResult, error)
	AdvanceAuthorization(ctx context.Context, req AdvanceAuthorizationRequest) (*AdvanceAuthorizationResult, error)
	SettleAuthorization(ctx context.Context, req SettleAuthorizationRequest) (*SettleAuthorizationResult, error)
	GetWholesaleAccount(ctx context.Context, payer []byte) (*WholesaleAccount, error)
	GetSpendAuthorization(ctx context.Context, payer []byte, authorizationID string) (*SpendAuthorizationStatus, error)
}

type FundWholesaleAccountResult struct {
	Account  *WholesaleAccount
	Credited *big.Int
	Replayed bool
}

type SpendAuthorizationStatus struct {
	State                      int32
	Reserved, Billed, Released *big.Int
	ActualUnits, SettlementSeq uint64
	ObservedAt                 string
}

type AdmitAuthorizationRequest struct {
	AuthorizationBytes []byte
	PaymentBytes       []byte
	Reservation        *big.Int
}

type AdvanceAuthorizationRequest struct {
	Payer           []byte
	AuthorizationID string
	CumulativeUnits uint64
	TargetReserved  *big.Int
	AdvanceSeq      uint64
	PaymentBytes    []byte
}

type AdvanceAuthorizationResult struct {
	State            int32
	Account          *WholesaleAccount
	BilledDelta      *big.Int
	CumulativeBilled *big.Int
	Reserved         *big.Int
	Credited         *big.Int
	Replayed         bool
}

type WholesaleAccount struct {
	Payer, Payee                           []byte
	Credited, Reserved, Debited, Available *big.Int
	Version                                uint64
	ObservedAt                             string
	ChainID                                uint64
	Denomination                           string
}

type AdmitAuthorizationResult struct {
	State    int32
	Account  *WholesaleAccount
	Reserved *big.Int
	Credited *big.Int
	Replayed bool
}

type SettleAuthorizationRequest struct {
	Payer           []byte
	AuthorizationID string
	ActualUnits     uint64
	SettlementSeq   uint64
}

type SettleAuthorizationResult struct {
	State    int32
	Account  *WholesaleAccount
	Billed   *big.Int
	Released *big.Int
	Replayed bool
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
