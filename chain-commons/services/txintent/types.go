// Package txintent provides the durable transaction state machine that
// every on-chain write across the monorepo uses.
//
// See docs/design-docs/tx-intent-state-machine.md for the full design
// (state diagram, persistence schema, idempotency, reorg, restart resume).
package txintent

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/chain"
	cerrors "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/errors"
)

// IntentID is a deterministic identifier derived from (Kind, KeyParams).
// Same logical operation always hashes to the same ID, providing
// idempotency across submits and across daemon restarts.
type IntentID [32]byte

// Hex returns the hex-encoded ID, used for logging.
func (id IntentID) Hex() string {
	return fmt.Sprintf("%x", id[:])
}

// String aliases Hex for fmt.Stringer.
func (id IntentID) String() string { return id.Hex() }

// Bytes returns the underlying byte slice.
func (id IntentID) Bytes() []byte { return id[:] }

// ComputeID hashes (kind, keyParams) into a stable IntentID.
func ComputeID(kind string, keyParams []byte) IntentID {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0x00}) // separator so adjacent kind/params don't collide
	h.Write(keyParams)
	var out IntentID
	copy(out[:], h.Sum(nil))
	return out
}

// IntentStatus is the state in the durable state machine.
type IntentStatus uint8

const (
	// StatusPending is the initial state — intent created, not yet signed.
	StatusPending IntentStatus = iota

	// StatusSigned means the tx is signed but not yet broadcast.
	StatusSigned

	// StatusSubmitted means the tx is broadcast; awaiting inclusion.
	StatusSubmitted

	// StatusMined means the tx is in a block; awaiting confirmations.
	StatusMined

	// StatusConfirmed is terminal: tx is N confirmations deep. Success.
	StatusConfirmed

	// StatusFailed is terminal: tx failed permanently (revert, nonce-past,
	// insufficient funds, replacement-exhausted).
	StatusFailed

	// StatusReplaced means the current attempt has been superseded by a
	// new attempt with bumped gas. The intent itself remains in flight; the
	// status transitions back to StatusSubmitted with a new attempt.
	// (Stored on Attempt, not on TxIntent — left here for symmetry.)
	StatusReplaced
)

// String returns the canonical name of the status.
func (s IntentStatus) String() string {
	switch s {
	case StatusPending:
		return "pending"
	case StatusSigned:
		return "signed"
	case StatusSubmitted:
		return "submitted"
	case StatusMined:
		return "mined"
	case StatusConfirmed:
		return "confirmed"
	case StatusFailed:
		return "failed"
	case StatusReplaced:
		return "replaced"
	default:
		return fmt.Sprintf("status(%d)", s)
	}
}

// IsTerminal reports whether the status is a terminal state.
func (s IntentStatus) IsTerminal() bool {
	return s == StatusConfirmed || s == StatusFailed
}

// Params is the input to Manager.Submit.
//
// (Kind, KeyParams) define idempotency: the same (Kind, KeyParams) submitted
// twice returns the same IntentID and never produces two transactions.
type Params struct {
	// Kind names the operation: "InitializeRound" | "RewardWithHint" |
	// "RedeemTicket" | "WriteServiceURI" | etc.
	Kind string

	// KeyParams is the canonical encoding of the subset of params that
	// define logical identity. Caller's responsibility (consumer knows
	// which param fields matter for dedup).
	KeyParams []byte

	// To is the destination contract address.
	To chain.Address

	// CallData is the ABI-encoded function call payload.
	CallData []byte

	// Value is wei sent with the tx (often zero for protocol calls).
	Value chain.Wei

	// GasLimit is the gas limit for this tx.
	GasLimit uint64

	// Metadata is optional ops-visibility info (round number, work ID, etc.)
	// surfaced via Status / List but not used by the state machine.
	Metadata map[string]string
}

// IntentAttempt records one signed-and-broadcast attempt at the same nonce.
// Multiple attempts arise from gas-bumping replacements.
type IntentAttempt struct {
	Nonce          uint64
	GasFeeCap      *big.Int
	GasTipCap      *big.Int
	SignedTxHash   chain.TxHash
	BroadcastedAt  time.Time
	MinedBlock     *chain.BlockNumber // set when seen in a block
	MinedBlockHash *chain.TxHash      // for reorg detection
	ReceiptStatus  *uint64            // 0 reverted | 1 success
	ReplacedAt     *time.Time         // set when this attempt is superseded
}

// TxIntent is the durable state record for one logical operation.
type TxIntent struct {
	ID            IntentID
	Kind          string
	KeyParams     []byte
	To            chain.Address
	CallData      []byte
	Value         *big.Int
	GasLimit      uint64
	Metadata      map[string]string

	Status        IntentStatus
	Attempts      []IntentAttempt

	CreatedAt     time.Time
	LastUpdatedAt time.Time
	ConfirmedAt   *time.Time

	FailedReason  *cerrors.Error
}

// CurrentAttempt returns the most recent (un-replaced) attempt, or nil if
// no attempts have been recorded yet.
func (t *TxIntent) CurrentAttempt() *IntentAttempt {
	for i := len(t.Attempts) - 1; i >= 0; i-- {
		if t.Attempts[i].ReplacedAt == nil {
			return &t.Attempts[i]
		}
	}
	return nil
}

// Filter is the predicate for List.
type Filter struct {
	Kinds    []string       // if non-empty, match any of these
	Statuses []IntentStatus // if non-empty, match any of these
	Since    *time.Time     // if non-nil, only intents created at/after
}
