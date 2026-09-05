// Package payoutpolicy decides whether a payout batch may be approved
// without a person.
//
// It mirrors sign-policy.json deliberately (plan 0044 §3.7): the same
// strict, fail-closed shape, the same hashed-into-audit discipline, the
// same pause file. Money leaving the pool is the one action nobody can
// undo, so the policy is written to refuse by default and to say why.
package payoutpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"
)

// Policy is payout-policy.json.
type Policy struct {
	// Shadow records what the policy WOULD have approved without
	// approving anything. It is how a pool earns the right to automate:
	// phase 0 of the graduation plan is four windows of shadow mode
	// with zero divergence from what humans actually approved.
	Shadow      bool        `json:"shadow"`
	AutoApprove AutoApprove `json:"auto_approve"`
}

type AutoApprove struct {
	Enabled bool `json:"enabled"`
	// MaxBatchWei and MaxPerMemberWei bound a single mistake. They are
	// decimal strings because a batch can exceed float64's exact range
	// long before it exceeds anyone's patience for rounding errors.
	MaxBatchWei     string `json:"max_batch_wei,omitempty"`
	MaxPerMemberWei string `json:"max_per_member_wei,omitempty"`
	// RequireScaleGTE refuses a batch derived from a window that did
	// not collect what it billed. A scale below one means the pool is
	// paying out more than it took in.
	RequireScaleGTE  float64 `json:"require_scale_gte,omitempty"`
	MaxBatchesPerDay int     `json:"max_batches_per_day,omitempty"`
}

// Decision is why a batch may or may not go automatically.
type Decision struct {
	Approved bool   `json:"approved"`
	Shadow   bool   `json:"shadow"`
	Reason   string `json:"reason"`
	// PolicyHash ties the decision to the exact policy that made it, so
	// an audit can prove which rules were in force at the time.
	PolicyHash string `json:"policy_hash"`
}

// Load reads and hashes the policy.
//
// A missing file is not an error: no policy means no automatic
// approval, which is the safe default and the state every pool starts
// in. A malformed one IS an error — a policy nobody can parse must not
// silently become a policy that approves nothing while looking
// configured.
func Load(path string) (Policy, string, error) {
	if strings.TrimSpace(path) == "" {
		return Policy{}, "", nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Policy{}, "", nil
		}
		return Policy{}, "", fmt.Errorf("read payout policy: %w", err)
	}
	var policy Policy
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&policy); err != nil {
		return Policy{}, "", fmt.Errorf("parse payout policy %s: %w", path, err)
	}
	if err := policy.Validate(); err != nil {
		return Policy{}, "", fmt.Errorf("payout policy %s: %w", path, err)
	}
	sum := sha256.Sum256(raw)
	return policy, hex.EncodeToString(sum[:]), nil
}

// Validate refuses a policy that would approve without a bound.
func (p Policy) Validate() error {
	if !p.AutoApprove.Enabled {
		return nil
	}
	// Enabling automatic approval with no ceiling is almost certainly a
	// half-written config rather than an intent to pay out any amount.
	if strings.TrimSpace(p.AutoApprove.MaxBatchWei) == "" {
		return fmt.Errorf("auto_approve.max_batch_wei is required when auto_approve is enabled")
	}
	if _, ok := new(big.Int).SetString(p.AutoApprove.MaxBatchWei, 10); !ok {
		return fmt.Errorf("auto_approve.max_batch_wei must be a decimal string")
	}
	if ref := strings.TrimSpace(p.AutoApprove.MaxPerMemberWei); ref != "" {
		if _, ok := new(big.Int).SetString(ref, 10); !ok {
			return fmt.Errorf("auto_approve.max_per_member_wei must be a decimal string")
		}
	}
	if p.AutoApprove.RequireScaleGTE < 0 || p.AutoApprove.RequireScaleGTE > 1 {
		return fmt.Errorf("auto_approve.require_scale_gte must be between 0 and 1")
	}
	if p.AutoApprove.MaxBatchesPerDay < 0 {
		return fmt.Errorf("auto_approve.max_batches_per_day must be >= 0")
	}
	return nil
}

// Batch is what a decision is made about.
type Batch struct {
	TotalWei        string
	MaxPerMemberWei string
	ScalePPM        uint64
	Anomaly         string
	// BatchesToday counts what has already gone out automatically, so a
	// runaway cannot empty the pool in one afternoon.
	BatchesToday int
}

// PauseFile is checked before every decision. Its presence is the kill
// switch: an operator who does not trust what automation is doing needs
// a way to stop it that does not require a deploy (as plan 0042).
func Paused(pausePath string) bool {
	if strings.TrimSpace(pausePath) == "" {
		return false
	}
	_, err := os.Stat(pausePath)
	return err == nil
}

// Evaluate decides one batch. It never approves on an error path: every
// refusal returns a reason, and an unrecognised condition refuses.
func Evaluate(policy Policy, hash string, batch Batch, pausePath string, now time.Time) Decision {
	refuse := func(reason string) Decision {
		return Decision{Approved: false, Shadow: policy.Shadow, Reason: reason, PolicyHash: hash}
	}
	if Paused(pausePath) {
		return refuse("paused: " + pausePath + " exists")
	}
	if !policy.AutoApprove.Enabled {
		return refuse("auto_approve is not enabled")
	}
	if strings.TrimSpace(batch.Anomaly) != "" {
		// An anomaly is exactly the case a human is for.
		return refuse("window has an attribution anomaly: " + batch.Anomaly)
	}
	if policy.AutoApprove.MaxBatchesPerDay > 0 && batch.BatchesToday >= policy.AutoApprove.MaxBatchesPerDay {
		return refuse(fmt.Sprintf("daily limit reached (%d)", policy.AutoApprove.MaxBatchesPerDay))
	}
	if policy.AutoApprove.RequireScaleGTE > 0 {
		scale := float64(batch.ScalePPM) / 1_000_000
		if scale < policy.AutoApprove.RequireScaleGTE {
			return refuse(fmt.Sprintf("settlement scale %.4f below required %.4f",
				scale, policy.AutoApprove.RequireScaleGTE))
		}
	}
	if over, err := exceeds(batch.TotalWei, policy.AutoApprove.MaxBatchWei); err != nil {
		return refuse("batch total is not a decimal amount: " + err.Error())
	} else if over {
		return refuse("batch total exceeds max_batch_wei")
	}
	if limit := strings.TrimSpace(policy.AutoApprove.MaxPerMemberWei); limit != "" {
		if over, err := exceeds(batch.MaxPerMemberWei, limit); err != nil {
			return refuse("per-member amount is not a decimal amount: " + err.Error())
		} else if over {
			return refuse("a member's amount exceeds max_per_member_wei")
		}
	}
	if policy.Shadow {
		// Shadow mode is the whole point of phase 0: record the verdict
		// and approve nothing, so divergence from human decisions is
		// measurable before it is trusted.
		return Decision{Approved: false, Shadow: true, PolicyHash: hash,
			Reason: "shadow mode: would have approved"}
	}
	return Decision{Approved: true, Shadow: false, PolicyHash: hash, Reason: "within policy"}
}

func exceeds(amount, limit string) (bool, error) {
	value, ok := new(big.Int).SetString(strings.TrimSpace(amount), 10)
	if !ok {
		return false, fmt.Errorf("%q", amount)
	}
	bound, ok := new(big.Int).SetString(strings.TrimSpace(limit), 10)
	if !ok {
		return false, fmt.Errorf("%q", limit)
	}
	return value.Cmp(bound) > 0, nil
}
