package settlement

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Closing a window is arithmetic over rounds that already reconciled,
// so it does not need a person. Deciding to pay out on a window that
// did not collect what it billed does — every hold below is a case
// where the pool would otherwise be distributing money it cannot show
// it received.

func TestEvaluateClose(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		window     types.SettlementWindow
		tolerance  float64
		wantClosed bool
		wantReason HoldReason
		why        string
	}{
		{
			// Everything billed was collected.
			name:       "a full scale closes",
			window:     types.SettlementWindow{ID: "w1", SettlementScalePPM: 1_000_000},
			tolerance:  0.01,
			wantClosed: true,
		},
		{
			// Inside the tolerance the operator allowed: a rounding
			// difference, not a discrepancy worth waking someone for.
			name:       "a scale inside the tolerance closes",
			window:     types.SettlementWindow{ID: "w2", SettlementScalePPM: 995_000},
			tolerance:  0.01,
			wantClosed: true,
		},
		{
			// Exactly at the boundary is inside it: the tolerance is
			// how far below one the pool said it would accept.
			name:       "a scale exactly at the tolerance boundary closes",
			window:     types.SettlementWindow{ID: "w3", SettlementScalePPM: 990_000},
			tolerance:  0.01,
			wantClosed: true,
		},
		{
			name:       "a scale below the tolerance is held",
			window:     types.SettlementWindow{ID: "w4", SettlementScalePPM: 989_999},
			tolerance:  0.01,
			wantReason: HoldScaleShort,
			why:        "below 1 - tolerance the pool would pay out more than it took in",
		},
		{
			name:       "a badly short scale is held",
			window:     types.SettlementWindow{ID: "w5", SettlementScalePPM: 500_000},
			tolerance:  0.01,
			wantReason: HoldScaleShort,
			why:        "the further below one, the more likely the cause is a bug than rounding",
		},
		{
			// Zero is not "everything was lost", it is "nothing has
			// reconciled yet". Closing on it would be closing on the
			// absence of evidence, and it would scale every member's
			// payout to nothing.
			name:       "a zero scale is held, not read as a total loss",
			window:     types.SettlementWindow{ID: "w6", SettlementScalePPM: 0},
			tolerance:  0.01,
			wantReason: HoldScaleShort,
			why:        "a zero scale means nothing reconciled, not that nothing was earned",
		},
		{
			// And a zero scale is held even with a tolerance wide
			// enough to swallow it, because the reason is missing
			// evidence rather than a shortfall.
			name:       "a zero scale is held even under a tolerance of one",
			window:     types.SettlementWindow{ID: "w7", SettlementScalePPM: 0},
			tolerance:  1,
			wantReason: HoldScaleShort,
			why:        "no tolerance setting can make absent evidence into evidence",
		},
		{
			name:       "an anomaly is held whatever the scale says",
			window:     types.SettlementWindow{ID: "w8", SettlementScalePPM: 1_000_000, Anomaly: "confirmed_revenue_below_attributed_revenue"},
			tolerance:  0.01,
			wantReason: HoldAnomaly,
			why:        "an anomaly is exactly the case a person is for",
		},
		{
			// The scale is also short, but the anomaly is the more
			// specific thing to tell the operator.
			name:       "an anomaly outranks a short scale in the reason given",
			window:     types.SettlementWindow{ID: "w9", SettlementScalePPM: 10_000, Anomaly: "duplicate_receipt"},
			tolerance:  0.01,
			wantReason: HoldAnomaly,
		},
		{
			name:       "an anomaly outranks a zero scale in the reason given",
			window:     types.SettlementWindow{ID: "w10", SettlementScalePPM: 0, Anomaly: "duplicate_receipt"},
			tolerance:  0.01,
			wantReason: HoldAnomaly,
		},
		{
			// A zero tolerance demands the full scale and nothing less.
			name:       "a zero tolerance holds anything under a full scale",
			window:     types.SettlementWindow{ID: "w11", SettlementScalePPM: 999_999},
			tolerance:  0,
			wantReason: HoldScaleShort,
		},
		{
			name:       "a zero tolerance closes on a full scale",
			window:     types.SettlementWindow{ID: "w12", SettlementScalePPM: 1_000_000},
			tolerance:  0,
			wantClosed: true,
		},
		{
			// A scale above one — the pool collected more than it
			// attributed — is not a reason to hold. It is not a
			// shortfall, and the surplus is handled downstream.
			name:       "a scale above one closes",
			window:     types.SettlementWindow{ID: "w13", SettlementScalePPM: 1_200_000},
			tolerance:  0.01,
			wantClosed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateClose(tc.window, tc.tolerance, now)
			if got.WindowID != tc.window.ID {
				t.Fatalf("WindowID = %q, want %q", got.WindowID, tc.window.ID)
			}
			if !got.At.Equal(now) {
				t.Fatalf("At = %s, want %s", got.At, now)
			}
			if tc.wantClosed {
				if !got.Closed || got.Held {
					t.Fatalf("EvaluateClose() = %+v, want a close", got)
				}
				if got.Reason != "" {
					t.Fatalf("a closed window carries hold reason %q", got.Reason)
				}
				return
			}
			if got.Closed {
				t.Fatalf("EvaluateClose() CLOSED: %+v (%s)", got, tc.why)
			}
			if !got.Held {
				t.Fatalf("EvaluateClose() neither closed nor held: %+v", got)
			}
			if got.Reason != tc.wantReason {
				t.Fatalf("Reason = %q, want %q", got.Reason, tc.wantReason)
			}
			// A hold with no detail is a window an operator has to
			// reverse-engineer before they can decide anything.
			if strings.TrimSpace(got.Detail) == "" {
				t.Fatalf("held with no detail: %+v", got)
			}
		})
	}
}

// TestEvaluateCloseClampsANegativeTolerance pins the direction a bad
// setting fails in. A negative tolerance read literally would require a
// scale ABOVE one — inverting the test into one that holds every
// healthy window — so it is clamped to zero, the strictest sane
// reading, rather than trusted.
func TestEvaluateCloseClampsANegativeTolerance(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	full := types.SettlementWindow{ID: "w1", SettlementScalePPM: 1_000_000}

	for _, tolerance := range []float64{-0.01, -1, -1000} {
		got := EvaluateClose(full, tolerance, now)
		if !got.Closed {
			t.Fatalf("tolerance %v held a fully reconciled window: %+v — a negative "+
				"tolerance must clamp to zero, not demand a scale above one", tolerance, got)
		}
	}

	// Clamped to zero, not to some permissive value: a short window is
	// still held.
	short := types.SettlementWindow{ID: "w2", SettlementScalePPM: 999_999}
	if got := EvaluateClose(short, -0.5, now); !got.Held || got.Reason != HoldScaleShort {
		t.Fatalf("EvaluateClose(short, -0.5) = %+v, want a hold: clamping must go to the "+
			"strict end, not turn a bad setting into a wide allowance", got)
	}
}
