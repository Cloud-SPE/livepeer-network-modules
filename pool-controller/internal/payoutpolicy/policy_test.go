// Tests for the policy that decides whether money may leave the pool
// without a person. Money leaving is the one action nobody can undo, so
// every assertion here is about a refusal being reached, being
// explained, and being reached for the right reason.
package payoutpolicy

import (
	"encoding/json"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writePolicyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payout-policy.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return path
}

func TestLoadMissingFileIsNoPolicyNotAnError(t *testing.T) {
	// Every pool starts here. No policy means no automatic approval,
	// which is the safe default — refusing to boot over it would punish
	// the operator who has not opted in.
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	policy, hash, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing) error = %v, want nil", err)
	}
	if policy != (Policy{}) {
		t.Fatalf("Load(missing) policy = %+v, want the zero policy", policy)
	}
	if hash != "" {
		t.Fatalf("Load(missing) hash = %q, want empty", hash)
	}
	// And the zero policy must actually refuse, not merely look empty.
	if decision := Evaluate(policy, hash, Batch{TotalWei: "1"}, "", time.Now()); decision.Approved {
		t.Fatal("the zero policy approved a batch")
	}
}

func TestLoadEmptyPathIsNoPolicy(t *testing.T) {
	policy, hash, err := Load("   ")
	if err != nil || policy != (Policy{}) || hash != "" {
		t.Fatalf("Load(blank) = %+v, %q, %v", policy, hash, err)
	}
}

func TestLoadRefusesWhatItCannotFullyUnderstand(t *testing.T) {
	cases := []struct {
		name string
		body string
		why  string
	}{
		{
			name: "malformed json",
			body: `{"shadow": true`,
			// A policy nobody can parse must not silently become a
			// policy that approves nothing while looking configured:
			// the operator would believe their bounds were in force.
			why: "unparseable policy must be an error, not an empty policy",
		},
		{
			// Strict on purpose, the same as sign-policy.json. A
			// misspelled key is how a bound goes missing: the operator
			// wrote a ceiling, the parser dropped it, and the policy
			// runs with no ceiling at all.
			name: "unknown field",
			body: `{"shadow": false, "auto_aprove": {"enabled": true}}`,
			why:  "an unknown key is a typo in a bound, not an extension",
		},
		{
			name: "misspelled field inside auto_approve",
			body: `{"auto_approve": {"enabled": true, "max_batch_we": "100"}}`,
			why:  "a misspelled ceiling must not be silently dropped",
		},
		{
			name: "wrong type for a bound",
			body: `{"auto_approve": {"enabled": true, "max_batch_wei": 100}}`,
			why:  "wei amounts are decimal strings; a JSON number cannot hold them exactly",
		},
		{
			name: "enabled with no batch ceiling",
			body: `{"auto_approve": {"enabled": true}}`,
			why:  "a half-written config must not read as \"any amount\"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, hash, err := Load(writePolicyFile(t, tc.body))
			if err == nil {
				t.Fatalf("Load() error = nil, want an error: %s", tc.why)
			}
			// A refused load must hand back nothing usable, so a caller
			// that ignores the error cannot end up holding a policy.
			if policy != (Policy{}) || hash != "" {
				t.Fatalf("Load() returned policy=%+v hash=%q alongside its error", policy, hash)
			}
		})
	}
}

func TestLoadReadsAWholePolicyAndHashesIt(t *testing.T) {
	body := `{
  "shadow": true,
  "auto_approve": {
    "enabled": true,
    "max_batch_wei": "1000000000000000000",
    "max_per_member_wei": "100000000000000000",
    "require_scale_gte": 0.99,
    "max_batches_per_day": 2
  }
}`
	path := writePolicyFile(t, body)
	policy, hash, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !policy.Shadow || !policy.AutoApprove.Enabled {
		t.Fatalf("policy = %+v", policy)
	}
	if policy.AutoApprove.MaxBatchWei != "1000000000000000000" ||
		policy.AutoApprove.MaxPerMemberWei != "100000000000000000" ||
		policy.AutoApprove.RequireScaleGTE != 0.99 ||
		policy.AutoApprove.MaxBatchesPerDay != 2 {
		t.Fatalf("policy = %+v", policy)
	}
	if len(hash) != 64 {
		t.Fatalf("hash = %q, want a 64-char sha256 hex digest", hash)
	}

	// Stable: the same bytes hash the same, every time. The hash is
	// what an audit uses to prove which rules were in force, so a hash
	// that drifted would make the trail unprovable.
	for i := 0; i < 3; i++ {
		_, again, err := Load(path)
		if err != nil || again != hash {
			t.Fatalf("re-Load() hash = %q (err %v), want the stable %q", again, err, hash)
		}
	}

	// And it moves when the content moves, or a changed bound would be
	// indistinguishable from the one it replaced.
	changed := writePolicyFile(t, strings.Replace(body, `"max_batches_per_day": 2`, `"max_batches_per_day": 3`, 1))
	_, otherHash, err := Load(changed)
	if err != nil {
		t.Fatalf("Load(changed) error = %v", err)
	}
	if otherHash == hash {
		t.Fatalf("a changed policy hashed identically: %q", hash)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		policy  Policy
		wantErr string
		why     string
	}{
		{
			// Nothing to bound when nothing is automatic.
			name:   "disabled policy needs no bounds",
			policy: Policy{},
		},
		{
			name:   "disabled policy is not judged on its other fields",
			policy: Policy{AutoApprove: AutoApprove{RequireScaleGTE: 5, MaxBatchesPerDay: -1}},
		},
		{
			name:    "enabled with no batch ceiling",
			policy:  Policy{AutoApprove: AutoApprove{Enabled: true}},
			wantErr: "max_batch_wei is required",
			why:     "enabling approval with no ceiling reads as \"any amount\"",
		},
		{
			name:    "enabled with a blank batch ceiling",
			policy:  Policy{AutoApprove: AutoApprove{Enabled: true, MaxBatchWei: "   "}},
			wantErr: "max_batch_wei is required",
		},
		{
			name:    "non-decimal batch ceiling",
			policy:  Policy{AutoApprove: AutoApprove{Enabled: true, MaxBatchWei: "1e18"}},
			wantErr: "max_batch_wei must be a decimal string",
			why:     "an exponent is not an exact wei amount",
		},
		{
			name:    "hex batch ceiling",
			policy:  Policy{AutoApprove: AutoApprove{Enabled: true, MaxBatchWei: "0x10"}},
			wantErr: "max_batch_wei must be a decimal string",
		},
		{
			name: "non-decimal per-member ceiling",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", MaxPerMemberWei: "ten"}},
			wantErr: "max_per_member_wei must be a decimal string",
		},
		{
			name: "an empty per-member ceiling is simply unset",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", MaxPerMemberWei: ""}},
		},
		{
			// A scale is a fraction of what was billed. Above one is
			// unreachable and below zero is meaningless, and either
			// spelling would make the gate silently unenforceable.
			name: "require_scale_gte above one",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", RequireScaleGTE: 1.0001}},
			wantErr: "require_scale_gte must be between 0 and 1",
		},
		{
			name: "require_scale_gte below zero",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", RequireScaleGTE: -0.1}},
			wantErr: "require_scale_gte must be between 0 and 1",
		},
		{
			name: "require_scale_gte of exactly one is the strictest legal setting",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", RequireScaleGTE: 1}},
		},
		{
			name: "negative daily limit",
			policy: Policy{AutoApprove: AutoApprove{
				Enabled: true, MaxBatchWei: "100", MaxBatchesPerDay: -1}},
			wantErr: "max_batches_per_day must be >= 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() error = nil, want one containing %q (%s)", tc.wantErr, tc.why)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Validate() error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// livePolicy approves within bounds, with no shadow.
func livePolicy() Policy {
	return Policy{AutoApprove: AutoApprove{
		Enabled:          true,
		MaxBatchWei:      "1000",
		MaxPerMemberWei:  "400",
		RequireScaleGTE:  0.95,
		MaxBatchesPerDay: 3,
	}}
}

func okBatch() Batch {
	return Batch{TotalWei: "900", MaxPerMemberWei: "300", ScalePPM: 1_000_000}
}

func TestEvaluateApprovesOnlyWithinEveryBound(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	decision := Evaluate(livePolicy(), "abc", okBatch(), "", now)
	if !decision.Approved {
		t.Fatalf("Evaluate() refused a batch inside every bound: %+v", decision)
	}
	if decision.Shadow {
		t.Fatalf("Shadow = true on a live policy: %+v", decision)
	}
	if decision.Reason != "within policy" {
		t.Fatalf("Reason = %q", decision.Reason)
	}
	// The hash rides on the decision so an audit can prove which rules
	// made it.
	if decision.PolicyHash != "abc" {
		t.Fatalf("PolicyHash = %q, want it carried through", decision.PolicyHash)
	}
}

func TestEvaluateRefusalPaths(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		policy     Policy
		batch      Batch
		pausePath  func(t *testing.T) string
		wantReason string
		why        string
	}{
		{
			name:   "paused by the kill-switch file",
			policy: livePolicy(),
			batch:  okBatch(),
			pausePath: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "payout.pause")
				if err := os.WriteFile(path, []byte("stop"), 0o644); err != nil {
					t.Fatalf("write pause file: %v", err)
				}
				return path
			},
			wantReason: "paused",
			// The kill switch has to work without a deploy, and it has
			// to outrank everything else — an operator who has stopped
			// automation has stopped it.
			why: "the pause file must be checked before any bound",
		},
		{
			name:       "auto approve not enabled",
			policy:     Policy{},
			batch:      okBatch(),
			wantReason: "auto_approve is not enabled",
		},
		{
			name:       "attribution anomaly",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "10", MaxPerMemberWei: "10", ScalePPM: 1_000_000, Anomaly: "confirmed_revenue_below_attributed_revenue"},
			wantReason: "attribution anomaly",
			why:        "an anomaly is exactly the case a human is for",
		},
		{
			name:       "daily limit reached",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "10", MaxPerMemberWei: "10", ScalePPM: 1_000_000, BatchesToday: 3},
			wantReason: "daily limit reached (3)",
			why:        "a runaway must not be able to empty the pool in one afternoon",
		},
		{
			name:       "daily limit exceeded",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "10", MaxPerMemberWei: "10", ScalePPM: 1_000_000, BatchesToday: 99},
			wantReason: "daily limit reached (3)",
		},
		{
			name:       "settlement scale below the required floor",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "10", MaxPerMemberWei: "10", ScalePPM: 940_000},
			wantReason: "below required",
			why:        "a scale under one means the pool would pay out money it never took in",
		},
		{
			name:       "batch total over the ceiling",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "1001", MaxPerMemberWei: "10", ScalePPM: 1_000_000},
			wantReason: "batch total exceeds max_batch_wei",
		},
		{
			name:       "one member over the per-member ceiling",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "900", MaxPerMemberWei: "401", ScalePPM: 1_000_000},
			wantReason: "exceeds max_per_member_wei",
			why:        "the per-member bound is about one member's exposure, not the average",
		},
		{
			name:       "malformed batch total",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "not-a-number", MaxPerMemberWei: "10", ScalePPM: 1_000_000},
			wantReason: "batch total is not a decimal amount",
			why:        "an amount that cannot be compared must refuse, never pass the comparison",
		},
		{
			name:       "empty batch total",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "", MaxPerMemberWei: "10", ScalePPM: 1_000_000},
			wantReason: "batch total is not a decimal amount",
		},
		{
			name:       "malformed per-member amount",
			policy:     livePolicy(),
			batch:      Batch{TotalWei: "900", MaxPerMemberWei: "0x1f", ScalePPM: 1_000_000},
			wantReason: "per-member amount is not a decimal amount",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pause := ""
			if tc.pausePath != nil {
				pause = tc.pausePath(t)
			}
			decision := Evaluate(tc.policy, "hash-1", tc.batch, pause, now)
			if decision.Approved {
				t.Fatalf("Evaluate() APPROVED: %+v (%s)", decision, tc.why)
			}
			if !strings.Contains(decision.Reason, tc.wantReason) {
				t.Fatalf("Reason = %q, want it to mention %q", decision.Reason, tc.wantReason)
			}
			// A refusal with no reason is not reviewable, and the whole
			// graduation plan is built on reading these back.
			if strings.TrimSpace(decision.Reason) == "" {
				t.Fatal("refused with no reason")
			}
			if decision.PolicyHash != "hash-1" {
				t.Fatalf("PolicyHash = %q, want it carried onto refusals too", decision.PolicyHash)
			}
		})
	}
}

// TestPausedOutranksEverything pins the ordering: the kill switch is
// checked first, so it stops even a batch that would otherwise sail
// through, and it stops shadow-mode recording too.
func TestPausedOutranksEverything(t *testing.T) {
	path := filepath.Join(t.TempDir(), "payout.pause")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("write pause file: %v", err)
	}
	if !Paused(path) {
		t.Fatal("Paused() = false with the file present")
	}
	decision := Evaluate(livePolicy(), "h", okBatch(), path, time.Now())
	if decision.Approved || !strings.Contains(decision.Reason, "paused") {
		t.Fatalf("Evaluate() = %+v, want a refusal naming the pause", decision)
	}

	// Removed: automation resumes. The switch has to be reversible
	// without a deploy in both directions.
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove pause file: %v", err)
	}
	if Paused(path) {
		t.Fatal("Paused() = true after the file was removed")
	}
	if decision := Evaluate(livePolicy(), "h", okBatch(), path, time.Now()); !decision.Approved {
		t.Fatalf("Evaluate() = %+v after unpausing, want approval", decision)
	}

	// No pause path configured at all is not "paused".
	if Paused("") || Paused("   ") {
		t.Fatal("Paused() = true with no pause path configured")
	}
}

// TestShadowModeApprovesNothingWhileReportingWhatItWould is the
// mechanism the whole graduation plan rests on. Phase 0 is four windows
// of shadow with zero divergence from human approvals — worth nothing
// if shadow mode ever actually approved.
func TestShadowModeApprovesNothingWhileReportingWhatItWould(t *testing.T) {
	policy := livePolicy()
	policy.Shadow = true
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	decision := Evaluate(policy, "hash-1", okBatch(), "", now)
	if decision.Approved {
		t.Fatal("shadow mode APPROVED a batch: nothing may be approved while shadowing")
	}
	if !decision.Shadow {
		t.Fatalf("Shadow = false on a shadow decision: %+v", decision)
	}
	if decision.Reason != "shadow mode: would have approved" {
		t.Fatalf("Reason = %q, want the would-have-approved verdict", decision.Reason)
	}
	if decision.PolicyHash != "hash-1" {
		t.Fatalf("PolicyHash = %q", decision.PolicyHash)
	}

	// A shadow refusal is still a refusal, and it must say what refused
	// it rather than "would have approved" — otherwise the divergence
	// measurement counts a refusal as an approval.
	over := okBatch()
	over.TotalWei = "100000"
	refused := Evaluate(policy, "hash-1", over, "", now)
	if refused.Approved {
		t.Fatal("shadow mode approved an over-limit batch")
	}
	if !refused.Shadow {
		t.Fatalf("Shadow = false on a shadow refusal: %+v", refused)
	}
	if !strings.Contains(refused.Reason, "exceeds max_batch_wei") {
		t.Fatalf("Reason = %q, want the actual bound that refused it", refused.Reason)
	}

	// The identical batch under the identical bounds, shadow off, is
	// approved — which is what makes the shadow verdict meaningful.
	policy.Shadow = false
	if live := Evaluate(policy, "hash-1", okBatch(), "", now); !live.Approved {
		t.Fatalf("the same batch was refused with shadow off: %+v", live)
	}
}

// TestWeiComparisonIsExactBeyondFloat64 is why the amounts are decimal
// strings compared with big.Int. Past 2^53 a float64 cannot represent
// consecutive integers, so a float comparison would read a batch one
// wei over its ceiling as exactly at it.
func TestWeiComparisonIsExactBeyondFloat64(t *testing.T) {
	// 10^18 wei is one ether; float64 loses exact integers above ~9.0e15.
	limit := "1000000000000000000"
	overByOne := "1000000000000000001"

	// Prove the premise rather than assume it: these two are the same
	// float64, so any float-based comparison cannot tell them apart.
	limitF, _ := new(big.Float).SetString(limit)
	overF, _ := new(big.Float).SetString(overByOne)
	l, _ := limitF.Float64()
	o, _ := overF.Float64()
	if l != o {
		t.Fatalf("premise broken: %v and %v differ as float64", l, o)
	}

	policy := Policy{AutoApprove: AutoApprove{Enabled: true, MaxBatchWei: limit}}
	now := time.Now().UTC()

	if decision := Evaluate(policy, "h", Batch{TotalWei: limit}, "", now); !decision.Approved {
		t.Fatalf("a batch exactly at the ceiling was refused: %+v", decision)
	}
	decision := Evaluate(policy, "h", Batch{TotalWei: overByOne}, "", now)
	if decision.Approved {
		t.Fatalf("a batch one wei over a 10^18 ceiling was APPROVED: %+v", decision)
	}
	if !strings.Contains(decision.Reason, "exceeds max_batch_wei") {
		t.Fatalf("Reason = %q", decision.Reason)
	}

	// Same story for the per-member bound, and at a size no float could
	// even approximate.
	huge := strings.Repeat("9", 40)
	hugePlus := "1" + strings.Repeat("0", 40)
	perMember := Policy{AutoApprove: AutoApprove{
		Enabled: true, MaxBatchWei: hugePlus, MaxPerMemberWei: huge}}
	if d := Evaluate(perMember, "h", Batch{TotalWei: "1", MaxPerMemberWei: huge}, "", now); !d.Approved {
		t.Fatalf("a per-member amount exactly at a 40-digit ceiling was refused: %+v", d)
	}
	if d := Evaluate(perMember, "h", Batch{TotalWei: "1", MaxPerMemberWei: hugePlus}, "", now); d.Approved {
		t.Fatalf("a per-member amount over a 40-digit ceiling was APPROVED: %+v", d)
	}
}

func TestEvaluateBoundsAreInclusiveAtTheEdge(t *testing.T) {
	now := time.Now().UTC()
	policy := livePolicy()

	// "exceeds" means strictly greater: a batch exactly at the ceiling
	// is inside the bound the operator wrote.
	edge := Batch{TotalWei: "1000", MaxPerMemberWei: "400", ScalePPM: 950_000}
	if d := Evaluate(policy, "h", edge, "", now); !d.Approved {
		t.Fatalf("Evaluate() refused a batch exactly at every bound: %+v", d)
	}

	// One wei past each of them, in turn.
	for _, tc := range []struct {
		name  string
		batch Batch
	}{
		{"total", Batch{TotalWei: "1001", MaxPerMemberWei: "400", ScalePPM: 950_000}},
		{"per member", Batch{TotalWei: "1000", MaxPerMemberWei: "401", ScalePPM: 950_000}},
		{"scale", Batch{TotalWei: "1000", MaxPerMemberWei: "400", ScalePPM: 949_999}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if d := Evaluate(policy, "h", tc.batch, "", now); d.Approved {
				t.Fatalf("Evaluate() approved one step past the %s bound: %+v", tc.name, d)
			}
		})
	}
}

func TestEvaluateSkipsUnsetBounds(t *testing.T) {
	now := time.Now().UTC()
	// require_scale_gte unset means the scale is not gated at all —
	// zero must not be read as "require a scale of at least zero and
	// therefore reject nothing" *or* as an accidental gate.
	policy := Policy{AutoApprove: AutoApprove{Enabled: true, MaxBatchWei: "1000"}}
	if d := Evaluate(policy, "h", Batch{TotalWei: "1", ScalePPM: 0}, "", now); !d.Approved {
		t.Fatalf("Evaluate() = %+v, want approval when no scale is required", d)
	}
	// max_batches_per_day unset means no daily rate limit.
	if d := Evaluate(policy, "h", Batch{TotalWei: "1", BatchesToday: 10_000}, "", now); !d.Approved {
		t.Fatalf("Evaluate() = %+v, want approval when no daily limit is set", d)
	}
	// max_per_member_wei unset means no per-member ceiling, and a
	// malformed per-member amount is then never even parsed.
	if d := Evaluate(policy, "h", Batch{TotalWei: "1", MaxPerMemberWei: "garbage"}, "", now); !d.Approved {
		t.Fatalf("Evaluate() = %+v, want approval when no per-member ceiling is set", d)
	}
}

// TestPolicyRoundTripsThroughJSON matters because the policy is served
// back out of GET /admin/v1/payout-policy: what an operator reads there
// has to be the policy that is actually in force.
func TestPolicyRoundTripsThroughJSON(t *testing.T) {
	original := livePolicy()
	original.Shadow = true
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var back Policy
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&back); err != nil {
		t.Fatalf("Decode(own output) error = %v — the strict loader cannot read what it writes", err)
	}
	if back != original {
		t.Fatalf("round trip = %+v, want %+v", back, original)
	}
}
