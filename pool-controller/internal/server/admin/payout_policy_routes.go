package admin

import (
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/payoutpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Automatic payout approval (plan 0044 §3.7).
//
// Approval stays human by default. What this adds is a policy that can
// take the decision within bounds an operator wrote down, a shadow mode
// that records what it WOULD have decided so the bounds can be earned
// rather than guessed, and a pause file that stops it without a deploy.

func registerPayoutPolicyRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	// What is in force right now, and its hash — the same hash that
	// appears in the audit trail beside every decision it made.
	mux.HandleFunc("GET /admin/v1/payout-policy", auth(func(w http.ResponseWriter, _ *http.Request) {
		policy, hash, err := payoutpolicy.Load(deps.PayoutPolicyPath)
		if err != nil {
			writeAdminJSON(w, nil, err)
			return
		}
		writeAdminJSON(w, struct {
			Path   string              `json:"path,omitempty"`
			Policy payoutpolicy.Policy `json:"policy"`
			Hash   string              `json:"policy_hash,omitempty"`
			Paused bool                `json:"paused"`
		}{
			Path: deps.PayoutPolicyPath, Policy: policy, Hash: hash,
			Paused: payoutpolicy.Paused(deps.PayoutPausePath),
		}, nil)
	}))

	// Evaluate a pending batch. This is the decision itself in shadow
	// and live mode alike: in shadow it records and refuses, which is
	// what makes divergence from human approvals measurable.
	mux.HandleFunc("POST /admin/v1/payout-batches/{id}/policy-review", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		batch, err := deps.Repo.GetPayoutBatch(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		policy, hash, err := payoutpolicy.Load(deps.PayoutPolicyPath)
		if err != nil {
			writeAdminJSON(w, nil, err)
			return
		}
		now := time.Now().UTC()
		decision := payoutpolicy.Evaluate(policy, hash, batchFacts(deps, batch, now), deps.PayoutPausePath, now)

		// Recorded whether or not it approved: a refusal is evidence
		// too, and shadow mode is worthless if its verdicts are not
		// durable.
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "payout_policy_decision",
			OccurredAt:   now,
			ResourceID:   batch.ID,
			ResourceType: "payout_batch",
			Details: map[string]any{
				"approved": decision.Approved, "shadow": decision.Shadow,
				"reason": decision.Reason, "policy_hash": decision.PolicyHash,
			},
		})

		if decision.Approved {
			// An approved batch has to be EXPORTED, not merely marked.
			// Materialising the intents is what actually moves money;
			// flipping the status alone would leave an auto-approved
			// batch sitting approved and never paying out, which is
			// worse than refusing it — the pool would believe it had
			// paid and the member would be waiting.
			//
			// This is deliberately the same sequence the human approval
			// path performs, so the two cannot drift into approving
			// different things.
			intents := materializePayoutIntents(batch, now)
			for _, intent := range intents {
				if err := deps.Repo.SavePayoutIntent(intent); err != nil {
					writeAdminJSON(w, nil, err)
					return
				}
			}
			batch.Status = types.PayoutBatchApproved
			// The policy is the approver, named as such: an audit that
			// could not tell an automatic approval from a person's
			// would make the graduation plan unmeasurable.
			batch.ApprovedBy = "payout-policy:" + decision.PolicyHash
			batch.ApprovedAt = now
			batch.UpdatedAt = now
			if err := deps.Repo.PutPayoutBatch(batch); err != nil {
				writeAdminJSON(w, nil, err)
				return
			}
			_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
				Kind:         "payout_batch_approved",
				OccurredAt:   now,
				Actor:        batch.ApprovedBy,
				ResourceID:   batch.ID,
				ResourceType: "payout_batch",
				Details: map[string]any{
					"settlement_window_id": batch.SettlementWindowID,
					"payout_intents":       len(intents),
					"automatic":            true,
				},
			})
		}
		writeAdminJSON(w, decision, nil)
	}))
}

// batchFacts gathers what the policy judges on.
func batchFacts(deps Deps, batch types.PayoutBatch, now time.Time) payoutpolicy.Batch {
	facts := payoutpolicy.Batch{
		TotalWei:        batch.TotalAmountWei,
		MaxPerMemberWei: largestLine(batch),
		BatchesToday:    deps.batchesApprovedSince(now.Add(-24 * time.Hour)),
	}
	if window, err := deps.Repo.GetSettlementWindow(batch.SettlementWindowID); err == nil {
		facts.ScalePPM = window.SettlementScalePPM
		facts.Anomaly = window.Anomaly
	}
	return facts
}

// largestLine is the biggest single member payment in the batch — the
// per-member ceiling is about one member's exposure, not the average.
func largestLine(batch types.PayoutBatch) string {
	largest := "0"
	for _, line := range batch.LineItems {
		if compareWei(line.AmountWei, largest) > 0 {
			largest = line.AmountWei
		}
	}
	return largest
}

// compareWei orders two decimal wei strings. A malformed amount sorts
// as smaller so it never wins "largest" and quietly raises the ceiling
// a per-member limit is meant to enforce.
func compareWei(left, right string) int {
	l, lok := new(big.Int).SetString(strings.TrimSpace(left), 10)
	r, rok := new(big.Int).SetString(strings.TrimSpace(right), 10)
	switch {
	case !lok:
		return -1
	case !rok:
		return 1
	default:
		return l.Cmp(r)
	}
}

func (d Deps) batchesApprovedSince(cutoff time.Time) int {
	batches, err := d.Repo.ListPayoutBatches()
	if err != nil {
		// Unknown is not zero. Reporting zero would hand the policy a
		// clean slate every time the store hiccuped, which is exactly
		// the direction a rate limit must not fail.
		return 1 << 30
	}
	count := 0
	for _, batch := range batches {
		if batch.Status == types.PayoutBatchApproved && batch.UpdatedAt.After(cutoff) {
			count++
		}
	}
	return count
}
