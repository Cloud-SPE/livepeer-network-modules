package admin

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/payoutpolicy"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Automatic payout approval. Approval stays human by default; what
// these routes add is a policy that can take the decision within bounds
// an operator wrote down, a shadow mode that records what it WOULD have
// decided, and a pause file that stops it without a deploy.

const withinBoundsPolicy = `{
  "shadow": false,
  "auto_approve": {
    "enabled": true,
    "max_batch_wei": "1000",
    "max_per_member_wei": "600",
    "require_scale_gte": 0.95,
    "max_batches_per_day": 5
  }
}`

func policyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payout-policy.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write payout policy: %v", err)
	}
	return path
}

// seedPayoutReviewRepo is one pending batch of 900 wei across two
// members, drawn from a fully reconciled window.
func seedPayoutReviewRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	if err := stateRepo.PutSettlementWindow(types.SettlementWindow{
		ID: "window-1", Status: types.SettlementWindowPendingApproval, SettlementScalePPM: 1_000_000,
	}); err != nil {
		t.Fatalf("PutSettlementWindow() error = %v", err)
	}
	if err := stateRepo.PutPayoutBatch(types.PayoutBatch{
		ID:                 "batch-1",
		SettlementWindowID: "window-1",
		Status:             types.PayoutBatchPendingApproval,
		TotalAmountWei:     "900",
		LineItems: []types.PayoutLineItem{
			{MemberEthAddress: "0xaaa", DestinationAddress: "0xaaa", AmountWei: "500"},
			{MemberEthAddress: "0xbbb", DestinationAddress: "0xbbb", AmountWei: "400"},
		},
	}); err != nil {
		t.Fatalf("PutPayoutBatch() error = %v", err)
	}
	return stateRepo
}

func payoutPolicyServer(t *testing.T, stateRepo *repo.StateRepo, policyPath, pausePath string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	Register(mux, Deps{
		Repo:             stateRepo,
		PayoutPolicyPath: policyPath,
		PayoutPausePath:  pausePath,
		WrapAuth:         func(next http.HandlerFunc) http.HandlerFunc { return next },
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func policyReview(t *testing.T, server *httptest.Server, batchID string) (int, payoutpolicy.Decision, []byte) {
	t.Helper()
	resp, err := http.Post(server.URL+"/admin/v1/payout-batches/"+batchID+"/policy-review",
		"application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatalf("POST policy-review error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var decision payoutpolicy.Decision
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &decision); err != nil {
			t.Fatalf("Unmarshal(decision) error = %v body = %s", err, string(body))
		}
	}
	return resp.StatusCode, decision, body
}

func policyDecisionEvents(t *testing.T, stateRepo *repo.StateRepo) []types.AuditEvent {
	t.Helper()
	events, err := stateRepo.ListAuditEventsFiltered("payout_policy_decision", "payout_batch", "batch-1", 0)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	return events
}

func TestPolicyReviewApprovesAndFlipsTheBatchWhenWithinPolicy(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	server := payoutPolicyServer(t, stateRepo, policyFile(t, withinBoundsPolicy), "")

	status, decision, body := policyReview(t, server, "batch-1")
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if !decision.Approved || decision.Shadow {
		t.Fatalf("decision = %+v, want a live approval", decision)
	}
	if decision.PolicyHash == "" {
		t.Fatal("decision carries no policy hash: an audit cannot prove which rules approved this")
	}
	batch, err := stateRepo.GetPayoutBatch("batch-1")
	if err != nil {
		t.Fatalf("GetPayoutBatch() error = %v", err)
	}
	if batch.Status != types.PayoutBatchApproved {
		t.Fatalf("batch status = %q, want approved", batch.Status)
	}

	events := policyDecisionEvents(t, stateRepo)
	if len(events) != 1 {
		t.Fatalf("payout_policy_decision events = %d, want 1", len(events))
	}
	details := events[0].Details
	if details["approved"] != true || details["shadow"] != false {
		t.Fatalf("audit details = %+v", details)
	}
	if details["policy_hash"] != decision.PolicyHash {
		t.Fatalf("audit policy_hash = %v, want %q", details["policy_hash"], decision.PolicyHash)
	}
	if reason, _ := details["reason"].(string); reason == "" {
		t.Fatalf("audit event carries no reason: %+v", details)
	}
}

func TestPolicyReviewRecordsItsRefusalsToo(t *testing.T) {
	// A refusal is evidence. Shadow mode is worthless if its verdicts
	// are not durable, and a live policy that quietly refused leaves an
	// operator wondering why nothing was approved.
	cases := []struct {
		name       string
		policy     string
		pause      func(t *testing.T) string
		mutate     func(t *testing.T, stateRepo *repo.StateRepo)
		wantReason string
	}{
		{
			name:       "no policy file configured at all",
			policy:     "",
			wantReason: "auto_approve is not enabled",
		},
		{
			name:       "policy present but not enabled",
			policy:     `{"shadow": false, "auto_approve": {"enabled": false}}`,
			wantReason: "auto_approve is not enabled",
		},
		{
			name:   "batch over the ceiling",
			policy: `{"auto_approve": {"enabled": true, "max_batch_wei": "100"}}`,
			// 900 wei against a ceiling of 100.
			wantReason: "batch total exceeds max_batch_wei",
		},
		{
			name: "a single member over the per-member ceiling",
			// The largest line is 500; the batch total of 900 is fine.
			// The per-member bound is about one member's exposure.
			policy:     `{"auto_approve": {"enabled": true, "max_batch_wei": "1000", "max_per_member_wei": "499"}}`,
			wantReason: "exceeds max_per_member_wei",
		},
		{
			name:   "the window did not collect what it billed",
			policy: withinBoundsPolicy,
			mutate: func(t *testing.T, stateRepo *repo.StateRepo) {
				if err := stateRepo.PutSettlementWindow(types.SettlementWindow{
					ID: "window-1", Status: types.SettlementWindowPendingApproval,
					SettlementScalePPM: 800_000,
				}); err != nil {
					t.Fatalf("PutSettlementWindow() error = %v", err)
				}
			},
			wantReason: "below required",
		},
		{
			name:   "the window is anomalous",
			policy: withinBoundsPolicy,
			mutate: func(t *testing.T, stateRepo *repo.StateRepo) {
				if err := stateRepo.PutSettlementWindow(types.SettlementWindow{
					ID: "window-1", Status: types.SettlementWindowPendingApproval,
					SettlementScalePPM: 1_000_000, Anomaly: "duplicate_receipt",
				}); err != nil {
					t.Fatalf("PutSettlementWindow() error = %v", err)
				}
			},
			wantReason: "attribution anomaly",
		},
		{
			name:   "automation is paused",
			policy: withinBoundsPolicy,
			pause: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "payout.pause")
				if err := os.WriteFile(path, nil, 0o644); err != nil {
					t.Fatalf("write pause file: %v", err)
				}
				return path
			},
			wantReason: "paused",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stateRepo := seedPayoutReviewRepo(t)
			if tc.mutate != nil {
				tc.mutate(t, stateRepo)
			}
			path := ""
			if tc.policy != "" {
				path = policyFile(t, tc.policy)
			}
			pause := ""
			if tc.pause != nil {
				pause = tc.pause(t)
			}
			server := payoutPolicyServer(t, stateRepo, path, pause)

			status, decision, body := policyReview(t, server, "batch-1")
			if status != http.StatusOK {
				t.Fatalf("status = %d body = %s", status, body)
			}
			if decision.Approved {
				t.Fatalf("decision = %+v, want a refusal", decision)
			}
			if decision.Reason == "" {
				t.Fatal("refused with no reason")
			}
			// The batch must be untouched: a refusal that still moved
			// the batch would be an approval with extra steps.
			batch, _ := stateRepo.GetPayoutBatch("batch-1")
			if batch.Status != types.PayoutBatchPendingApproval {
				t.Fatalf("batch status = %q after a refusal, want pending_approval", batch.Status)
			}
			// And it is recorded anyway.
			events := policyDecisionEvents(t, stateRepo)
			if len(events) != 1 {
				t.Fatalf("payout_policy_decision events = %d, want 1 even for a refusal", len(events))
			}
			if events[0].Details["approved"] != false {
				t.Fatalf("audit approved = %v, want false", events[0].Details["approved"])
			}
			if reason, _ := events[0].Details["reason"].(string); reason != decision.Reason {
				t.Fatalf("audit reason = %q, want the served reason %q", reason, decision.Reason)
			}
		})
	}
}

// TestPolicyReviewInShadowModeApprovesNothing is the mechanism the
// graduation plan rests on: four windows of shadow with zero divergence
// from human approvals. If shadow mode ever moved a batch, phase 0
// would be paying out while it measured.
func TestPolicyReviewInShadowModeApprovesNothing(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	shadow := `{
  "shadow": true,
  "auto_approve": {
    "enabled": true,
    "max_batch_wei": "1000",
    "max_per_member_wei": "600",
    "require_scale_gte": 0.95,
    "max_batches_per_day": 5
  }
}`
	server := payoutPolicyServer(t, stateRepo, policyFile(t, shadow), "")

	status, decision, body := policyReview(t, server, "batch-1")
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %s", status, body)
	}
	if decision.Approved {
		t.Fatal("shadow mode APPROVED a batch")
	}
	if !decision.Shadow {
		t.Fatalf("decision = %+v, want Shadow true", decision)
	}
	if decision.Reason != "shadow mode: would have approved" {
		t.Fatalf("reason = %q, want the would-have-approved verdict", decision.Reason)
	}
	batch, _ := stateRepo.GetPayoutBatch("batch-1")
	if batch.Status != types.PayoutBatchPendingApproval {
		t.Fatalf("batch status = %q, want it left for a human", batch.Status)
	}

	// The verdict has to be durable, or there is nothing to compare
	// against the human decisions at the end of the phase.
	events := policyDecisionEvents(t, stateRepo)
	if len(events) != 1 {
		t.Fatalf("payout_policy_decision events = %d, want 1", len(events))
	}
	details := events[0].Details
	if details["shadow"] != true || details["approved"] != false {
		t.Fatalf("audit details = %+v, want shadow true / approved false", details)
	}
	if details["reason"] != "shadow mode: would have approved" {
		t.Fatalf("audit reason = %v", details["reason"])
	}
}

func TestPolicyReviewOnAnUnknownBatchIs404(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	server := payoutPolicyServer(t, stateRepo, policyFile(t, withinBoundsPolicy), "")
	if status, _, _ := policyReview(t, server, "batch-nope"); status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

func TestPayoutPolicyRouteReportsWhatIsInForce(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	pause := filepath.Join(t.TempDir(), "payout.pause")
	path := policyFile(t, withinBoundsPolicy)
	server := payoutPolicyServer(t, stateRepo, path, pause)

	read := func() struct {
		Path   string              `json:"path"`
		Policy payoutpolicy.Policy `json:"policy"`
		Hash   string              `json:"policy_hash"`
		Paused bool                `json:"paused"`
	} {
		t.Helper()
		resp, err := http.Get(server.URL + "/admin/v1/payout-policy")
		if err != nil {
			t.Fatalf("GET /admin/v1/payout-policy error = %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d body = %s", resp.StatusCode, string(body))
		}
		var out struct {
			Path   string              `json:"path"`
			Policy payoutpolicy.Policy `json:"policy"`
			Hash   string              `json:"policy_hash"`
			Paused bool                `json:"paused"`
		}
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("Unmarshal error = %v body = %s", err, string(body))
		}
		return out
	}

	view := read()
	if view.Path != path || !view.Policy.AutoApprove.Enabled || view.Hash == "" {
		t.Fatalf("view = %+v", view)
	}
	if view.Paused {
		t.Fatal("paused = true with no pause file present")
	}

	// The kill switch has to be visible to whoever is deciding whether
	// to trust automation, not just effective inside it.
	if err := os.WriteFile(pause, nil, 0o644); err != nil {
		t.Fatalf("write pause file: %v", err)
	}
	if !read().Paused {
		t.Fatal("paused = false with the pause file present")
	}
}

// TestPayoutPolicyRouteSurfacesABrokenPolicy: a policy the loader
// refuses must not read back as "no policy configured", or an operator
// would take a typo for a deliberate default.
func TestPayoutPolicyRouteSurfacesABrokenPolicy(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	server := payoutPolicyServer(t, stateRepo, policyFile(t, `{"auto_aprove": {}}`), "")
	resp, err := http.Get(server.URL + "/admin/v1/payout-policy")
	if err != nil {
		t.Fatalf("GET error = %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("status = 200 body = %s, want an error surfaced", string(body))
	}
}

// TestBatchesApprovedSinceCountsOnlyRecentApprovals covers the input to
// the daily rate limit.
func TestBatchesApprovedSinceCountsOnlyRecentApprovals(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = stateRepo.Close() })
	now := time.Now().UTC()

	for _, batch := range []types.PayoutBatch{
		{ID: "approved-1", Status: types.PayoutBatchApproved},
		{ID: "approved-2", Status: types.PayoutBatchApproved},
		// Not approved: a pending or paid batch is not something the
		// rate limit is counting.
		{ID: "pending-1", Status: types.PayoutBatchPendingApproval},
		{ID: "paid-1", Status: types.PayoutBatchPaid},
	} {
		if err := stateRepo.PutPayoutBatch(batch); err != nil {
			t.Fatalf("PutPayoutBatch() error = %v", err)
		}
	}
	deps := Deps{Repo: stateRepo}
	if got := deps.batchesApprovedSince(now.Add(-24 * time.Hour)); got != 2 {
		t.Fatalf("batchesApprovedSince(24h ago) = %d, want 2", got)
	}
	// A cutoff in the future excludes everything: PutPayoutBatch stamps
	// UpdatedAt with now, so nothing is after it.
	if got := deps.batchesApprovedSince(now.Add(time.Hour)); got != 0 {
		t.Fatalf("batchesApprovedSince(future) = %d, want 0", got)
	}
}

// TestBatchesApprovedSinceFailsClosedWhenTheStoreErrors is the one that
// matters most here.
//
// Reporting zero on a store error would hand the daily rate limit a
// clean slate every time the store hiccuped — so a flapping store would
// look exactly like "no batches have gone out today", and automation
// would keep approving. A rate limit must fail in the direction of
// refusing, so an unreadable store reports a number no configured limit
// can be above.
func TestBatchesApprovedSinceFailsClosedWhenTheStoreErrors(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("repo.Open() error = %v", err)
	}
	// A closed repo is how the store errors: ListPayoutBatches returns
	// "repo is not open".
	if err := stateRepo.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := stateRepo.ListPayoutBatches(); err == nil {
		t.Fatal("premise broken: ListPayoutBatches() on a closed repo returned no error")
	}

	got := Deps{Repo: stateRepo}.batchesApprovedSince(time.Now().Add(-24 * time.Hour))
	if got == 0 {
		t.Fatal("batchesApprovedSince() reported 0 on a store error: that hands the rate " +
			"limit a clean slate exactly when it cannot verify one")
	}
	if got != 1<<30 {
		t.Fatalf("batchesApprovedSince() = %d, want %d", got, 1<<30)
	}
	// And the number has to actually beat any limit an operator would
	// plausibly write down, not merely be non-zero.
	if got <= 1_000_000 {
		t.Fatalf("batchesApprovedSince() = %d, too small to be above a configured daily limit", got)
	}
}

// TestPolicyReviewRefusesOnceTheDailyLimitIsReached joins that number
// to the decision it feeds.
func TestPolicyReviewRefusesOnceTheDailyLimitIsReached(t *testing.T) {
	stateRepo := seedPayoutReviewRepo(t)
	// Two already approved today against a limit of two.
	for _, id := range []string{"earlier-1", "earlier-2"} {
		if err := stateRepo.PutPayoutBatch(types.PayoutBatch{
			ID: id, Status: types.PayoutBatchApproved, TotalAmountWei: "1",
		}); err != nil {
			t.Fatalf("PutPayoutBatch() error = %v", err)
		}
	}
	policy := `{"auto_approve": {"enabled": true, "max_batch_wei": "1000", "max_batches_per_day": 2}}`
	server := payoutPolicyServer(t, stateRepo, policyFile(t, policy), "")

	_, decision, _ := policyReview(t, server, "batch-1")
	if decision.Approved {
		t.Fatalf("decision = %+v, want a refusal at the daily limit", decision)
	}
	if decision.Reason != "daily limit reached (2)" {
		t.Fatalf("reason = %q", decision.Reason)
	}
	batch, _ := stateRepo.GetPayoutBatch("batch-1")
	if batch.Status != types.PayoutBatchPendingApproval {
		t.Fatalf("batch status = %q", batch.Status)
	}
}

// TestLargestLineIgnoresAMalformedAmount: a malformed amount must not
// win "largest" and quietly raise the ceiling the per-member limit is
// meant to enforce.
func TestLargestLineIgnoresAMalformedAmount(t *testing.T) {
	batch := types.PayoutBatch{LineItems: []types.PayoutLineItem{
		{AmountWei: "100"},
		{AmountWei: "not-a-number"},
		{AmountWei: "250"},
	}}
	if got := largestLine(batch); got != "250" {
		t.Fatalf("largestLine() = %q, want %q", got, "250")
	}
	// With no usable amounts at all, "0" — not the garbage string,
	// which would then fail the policy's decimal parse and refuse. Both
	// are safe; this pins which one happens.
	only := types.PayoutBatch{LineItems: []types.PayoutLineItem{{AmountWei: "bad"}}}
	if got := largestLine(only); got != "0" {
		t.Fatalf("largestLine(all malformed) = %q, want %q", got, "0")
	}
	if got := largestLine(types.PayoutBatch{}); got != "0" {
		t.Fatalf("largestLine(no lines) = %q, want %q", got, "0")
	}
}
