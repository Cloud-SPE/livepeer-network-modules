// Package errors holds the classified error types used across chain-commons.
//
// Consumers use Classify() to decide between retry, fail-fast, or surface
// behaviour. Provider implementations return *Error values from any operation
// that can fail.
//
// See docs/design-docs/multi-rpc-failover.md and tx-intent-state-machine.md
// for the per-class semantics.
package errors

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorClass categorizes errors by recovery story.
type ErrorClass int

const (
	// ClassUnknown is the zero value; treated as Transient by retry policy
	// (conservative default).
	ClassUnknown ErrorClass = iota

	// ClassTransient indicates the operation may succeed if retried after a
	// backoff (network blip, RPC overload, ratelimited).
	ClassTransient

	// ClassPermanent indicates the operation will not succeed without changes
	// to the inputs (revert, invalid params, malformed calldata).
	ClassPermanent

	// ClassReverted indicates the transaction mined but reverted on-chain.
	// Often a permanent application-layer error (e.g., reward called when
	// already-rewarded).
	ClassReverted

	// ClassNoncePast indicates the transaction's nonce has already been used
	// by another transaction. The intent is stale; consumers may resubmit
	// with a fresh intent (which will get a new nonce).
	ClassNoncePast

	// ClassInsufficientFunds indicates the wallet's balance is below the
	// transaction's cost. Operator action required; surface to ops.
	ClassInsufficientFunds

	// ClassReorged indicates a previously-mined transaction is no longer in
	// the canonical chain. The intent state machine handles recovery.
	ClassReorged

	// ClassCircuitOpen indicates all configured RPC endpoints have open
	// circuit breakers; no upstream is healthy. Treated as transient by
	// callers but surfaces a distinct error code for ops dashboards.
	ClassCircuitOpen
)

// String returns the canonical name of the class.
func (c ErrorClass) String() string {
	switch c {
	case ClassUnknown:
		return "unknown"
	case ClassTransient:
		return "transient"
	case ClassPermanent:
		return "permanent"
	case ClassReverted:
		return "reverted"
	case ClassNoncePast:
		return "nonce_past"
	case ClassInsufficientFunds:
		return "insufficient_funds"
	case ClassReorged:
		return "reorged"
	case ClassCircuitOpen:
		return "circuit_open"
	default:
		return fmt.Sprintf("class(%d)", c)
	}
}

// Error is the canonical chain-commons error.
//
// Code is a stable string identifier (lowercase dot-separated, e.g.
// "rpc.timeout", "tx.reverted") used for log lines and metrics labels.
// Class drives the retry policy. Cause is the wrapped underlying error.
type Error struct {
	Class ErrorClass
	Code  string
	Msg   string
	Cause error
}

// New constructs an *Error with the given class, code, and message.
func New(class ErrorClass, code, msg string) *Error {
	return &Error{Class: class, Code: code, Msg: msg}
}

// Wrap constructs an *Error wrapping an underlying cause.
func Wrap(class ErrorClass, code, msg string, cause error) *Error {
	return &Error{Class: class, Code: code, Msg: msg, Cause: cause}
}

// Error implements the error interface.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Msg, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Msg)
}

// Unwrap returns the underlying cause.
func (e *Error) Unwrap() error { return e.Cause }

// Is reports whether the target is also an *Error with the same Code.
func (e *Error) Is(target error) bool {
	var te *Error
	if !errors.As(target, &te) {
		return false
	}
	return e.Code == te.Code
}

// Classify inspects an arbitrary error and returns an *Error with a best-
// effort classification. Already-classified *Error values are returned
// unchanged. Unknown errors classify as ClassTransient (conservative
// default — see "Decisions log" in multi-rpc-failover.md).
func Classify(err error) *Error {
	if err == nil {
		return nil
	}

	var ce *Error
	if errors.As(err, &ce) {
		return ce
	}

	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "nonce too low"),
		strings.Contains(msg, "nonce already used"):
		return Wrap(ClassNoncePast, "tx.nonce_past", "transaction nonce is stale", err)

	case strings.Contains(msg, "insufficient funds"):
		return Wrap(ClassInsufficientFunds, "tx.insufficient_funds", "wallet balance insufficient for transaction", err)

	case strings.Contains(msg, "execution reverted"),
		strings.Contains(msg, "vm execution error"):
		return Wrap(ClassReverted, "tx.reverted", "transaction mined but reverted on-chain", err)

	case strings.Contains(msg, "invalid argument"),
		strings.Contains(msg, "invalid params"):
		return Wrap(ClassPermanent, "rpc.invalid_argument", "invalid argument to RPC call", err)

	case strings.Contains(msg, "eof"),
		strings.Contains(msg, "connection reset"),
		strings.Contains(msg, "use of closed connection"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "tls: use of closed connection"):
		return Wrap(ClassTransient, "rpc.connection_error", "connection error to RPC", err)

	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "timeout"):
		return Wrap(ClassTransient, "rpc.timeout", "RPC call timed out", err)

	case strings.Contains(msg, "429"),
		strings.Contains(msg, "rate limit"),
		strings.Contains(msg, "too many requests"):
		return Wrap(ClassTransient, "rpc.rate_limited", "RPC endpoint rate-limited", err)

	case strings.Contains(msg, "unsupported block number"),
		strings.Contains(msg, "block not found"):
		return Wrap(ClassTransient, "rpc.block_not_synced", "RPC node has not yet synced the requested block", err)

	default:
		return Wrap(ClassTransient, "rpc.unknown", "unclassified error from RPC", err)
	}
}

// IsTransient reports whether err's classification is ClassTransient.
// Convenience for retry decisions.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	c := Classify(err)
	return c.Class == ClassTransient
}

// IsPermanent reports whether err's classification is ClassPermanent,
// ClassReverted, ClassNoncePast, or ClassInsufficientFunds — anything that
// retry won't fix.
func IsPermanent(err error) bool {
	if err == nil {
		return false
	}
	c := Classify(err)
	switch c.Class {
	case ClassPermanent, ClassReverted, ClassNoncePast, ClassInsufficientFunds:
		return true
	default:
		return false
	}
}
