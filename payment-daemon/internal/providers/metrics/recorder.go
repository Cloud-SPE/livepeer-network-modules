// Package metrics is the payment-daemon's observability surface. Every
// domain (receiver, settlement, escrow, chain providers, sender) emits
// through the Recorder interface; implementations decide how to record.
//
// Two implementations:
//
//   - Prometheus (production): writes to a private *prometheus.Registry
//     and serves the standard /metrics exposition. Installed when
//     --metrics-listen is set.
//   - Noop (default): zero-cost no-op; Handler returns 404 so an
//     operator who points Prometheus at a daemon without --metrics-listen
//     gets a clear "no metrics here" signal rather than an empty scrape.
//
// All label values are bounded by construction (role, method, result,
// reason, event). Per the broker's cardinality rule, no metric is ever
// labeled by sender address, work_id, ticket hash, or nonce.
package metrics

import (
	"math/big"
	"net/http"
	"time"
)

// Recorder is the single metrics surface for the payment-daemon. New
// emissions add a method here and wire it in every implementation.
type Recorder interface {
	// ----- gRPC (both roles) -----

	// IncGRPCRequest counts one completed gRPC request. role is
	// "receiver" or "sender"; code is the gRPC status code string.
	IncGRPCRequest(role, method, code string)
	// ObserveGRPC records the unary handler latency.
	ObserveGRPC(role, method string, d time.Duration)
	// SetGRPCInFlight reports the current in-flight count for (role, method).
	SetGRPCInFlight(role, method string, n int)

	// ----- Receiver domain -----

	// IncSessionEvent counts a session lifecycle transition:
	// "opened", "already_open", or "closed".
	IncSessionEvent(event string)
	// IncTicket counts one processed ticket: "accepted" or "rejected".
	IncTicket(result string)
	// IncTicketRejected counts one rejected ticket by reason
	// ("invalid_recipient_rand", "nonce_replay", "nonce_cap",
	// "invalid_signature", "other").
	IncTicketRejected(reason string)
	// IncWinningTicket counts one winning ticket queued for redemption.
	IncWinningTicket()
	// AddCreditedEVGwei adds credited expected value (in gwei) to the
	// running total. Use WeiToGwei to convert from wei.
	AddCreditedEVGwei(gwei float64)
	// IncDebit counts one DebitBalance call ("ok" or "error").
	IncDebit(result string)
	// AddWorkUnitsDebited adds debited work units to the running total.
	AddWorkUnitsDebited(units float64)

	// ----- Settlement / redemption loop -----

	// IncRedemption counts one redemption attempt outcome ("redeemed",
	// "expired", "already_used", "face_value_too_low",
	// "insufficient_funds", "tx_error").
	IncRedemption(result string)
	// ObserveRedemption records redemption attempt latency.
	ObserveRedemption(d time.Duration)
	// SetRedemptionQueueDepth reports the pending-redemption queue depth.
	SetRedemptionQueueDepth(n int)
	// IncRedemptionTx counts a redemption tx phase ("submitted",
	// "confirmed", "failed").
	IncRedemptionTx(result string)
	// SetGasPriceWei reports the most-recent gas price (wei) the
	// settlement loop observed.
	SetGasPriceWei(wei float64)
	// SetCurrentRound reports the last-initialized Livepeer round.
	SetCurrentRound(round int64)

	// ----- Escrow -----

	// SetEscrowPendingFloatWei reports total pending float across senders.
	SetEscrowPendingFloatWei(wei float64)
	// SetTrackedSenders reports the number of senders escrow is tracking.
	SetTrackedSenders(n int)
	// IncEscrowRebuild counts one escrow rebuild from the store.
	IncEscrowRebuild()

	// ----- Chain provider (via Broker decorator) -----

	// IncChainRead counts a chain read by method and result ("ok"/"error").
	IncChainRead(method, result string)
	// ObserveChainRead records chain read latency by method.
	ObserveChainRead(method string, d time.Duration)
	// IncChainWrite counts a chain write by method and result.
	IncChainWrite(method, result string)
	// SetChainLastSuccess stamps the time of the most recent successful
	// chain interaction.
	SetChainLastSuccess(t time.Time)

	// ----- Sender domain -----

	// IncPaymentCreated counts one CreatePayment outcome ("ok"/"error").
	IncPaymentCreated(result string)
	// IncTicketSigned counts one signed ticket.
	IncTicketSigned()
	// IncTicketParamsFetch counts one ticket-params HTTP fetch ("ok"/"error").
	IncTicketParamsFetch(result string)
	// ObserveTicketParamsFetch records ticket-params fetch latency.
	ObserveTicketParamsFetch(d time.Duration)
	// SetSenderSessions reports the number of cached sender sessions.
	SetSenderSessions(n int)
	// SetSenderDepositWei / SetSenderReserveWei report on-chain sender funds.
	SetSenderDepositWei(wei float64)
	SetSenderReserveWei(wei float64)

	// ----- Daemon-level -----

	SetUptimeSeconds(s float64)
	SetBuildInfo(version, mode, goVersion string)

	// ----- Exposition -----

	// Handler returns the http.Handler serving Prometheus exposition.
	Handler() http.Handler
}

// Label sentinels — use these constants at call sites so a typo fails
// to compile rather than silently creating a new series.
const (
	RoleReceiver = "receiver"
	RoleSender   = "sender"

	SessionOpened      = "opened"
	SessionAlreadyOpen = "already_open"
	SessionClosed      = "closed"

	TicketAccepted = "accepted"
	TicketRejected = "rejected"

	ReasonInvalidRecipientRand = "invalid_recipient_rand"
	ReasonNonceReplay          = "nonce_replay"
	ReasonNonceCap             = "nonce_cap"
	ReasonInvalidSignature     = "invalid_signature"
	ReasonOther                = "other"

	RedeemRedeemed         = "redeemed"
	RedeemExpired          = "expired"
	RedeemAlreadyUsed      = "already_used"
	RedeemFaceValueTooLow  = "face_value_too_low"
	RedeemInsufficientFund = "insufficient_funds"
	RedeemTxError          = "tx_error"

	TxSubmitted = "submitted"
	TxConfirmed = "confirmed"
	TxFailed    = "failed"

	ResultOK    = "ok"
	ResultError = "error"

	// LabelUnset is emitted for empty label values so Prometheus does
	// not reject empty strings.
	LabelUnset = "_unset_"
)

var weiPerGwei = big.NewFloat(1e9)

// WeiToGwei converts a wei amount to gwei as a float64 suitable for a
// metric. Precision loss is acceptable for observability. nil → 0.
func WeiToGwei(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	g := new(big.Float).Quo(new(big.Float).SetInt(wei), weiPerGwei)
	f, _ := g.Float64()
	return f
}

// WeiToFloat converts a wei amount to a float64 (wei units). nil → 0.
func WeiToFloat(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).SetInt(wei).Float64()
	return f
}

// unset maps an empty label value to LabelUnset.
func unset(v string) string {
	if v == "" {
		return LabelUnset
	}
	return v
}
