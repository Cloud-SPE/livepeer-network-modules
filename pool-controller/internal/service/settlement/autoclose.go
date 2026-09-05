package settlement

import (
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Automatic window close (plan 0044 §3.7).
//
// Closing a window is arithmetic over rounds that have already
// reconciled, so it does not need a person. Deciding to PAY OUT on a
// window that did not collect what it billed does, which is why a short
// or anomalous window is held rather than closed quietly.

// HoldReason says why a window needs a human. Empty means it does not.
type HoldReason string

const (
	HoldScaleShort HoldReason = "settlement_scale_below_tolerance"
	HoldAnomaly    HoldReason = "attribution_anomaly"
)

// AutoCloseDecision is what the closer concluded about one window.
type AutoCloseDecision struct {
	WindowID string     `json:"window_id"`
	Closed   bool       `json:"closed"`
	Held     bool       `json:"held"`
	Reason   HoldReason `json:"reason,omitempty"`
	Detail   string     `json:"detail,omitempty"`
	At       time.Time  `json:"at"`
}

// EvaluateClose decides whether a window may close on its own.
//
// tolerance is how far below a scale of 1.0 the pool will accept
// without asking. A scale of 1.0 means every wei billed was collected;
// below it the pool would be paying out money it did not receive, and
// the further below, the more likely the cause is a bug rather than a
// rounding difference.
func EvaluateClose(window types.SettlementWindow, tolerance float64, now time.Time) AutoCloseDecision {
	decision := AutoCloseDecision{WindowID: window.ID, At: now}
	if window.Anomaly != "" {
		decision.Held = true
		decision.Reason = HoldAnomaly
		decision.Detail = window.Anomaly
		return decision
	}
	// A scale of zero means nothing was reconciled yet, not that
	// everything was lost — closing on it would be closing on absence
	// of evidence.
	if window.SettlementScalePPM == 0 {
		decision.Held = true
		decision.Reason = HoldScaleShort
		decision.Detail = "no settlement scale recorded"
		return decision
	}
	if tolerance < 0 {
		tolerance = 0
	}
	scale := float64(window.SettlementScalePPM) / 1_000_000
	if scale < 1-tolerance {
		decision.Held = true
		decision.Reason = HoldScaleShort
		decision.Detail = fmt.Sprintf("scale %.4f below %.4f", scale, 1-tolerance)
		return decision
	}
	decision.Closed = true
	return decision
}
