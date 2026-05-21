package autoapprove

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func newRepo(t *testing.T) *repo.StateRepo {
	t.Helper()
	r, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func seedRerankOffer(t *testing.T, r *repo.StateRepo) types.Offer {
	t.Helper()
	offer := types.Offer{
		ID:              "offer-rerank",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
		Status:          types.OfferStatusActive,
	}
	if err := r.PutOffer(offer); err != nil {
		t.Fatalf("PutOffer() error = %v", err)
	}
	return offer
}

func approvableJoinRequest(id string) types.JoinRequest {
	return types.JoinRequest{
		ID:               id,
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		Status:           types.JoinRequestPending,
		RequestedBackends: []types.RequestedBackend{{
			ID:                 "backend-" + id,
			Transport:          "http",
			URL:                "http://backend-" + id,
			VerificationStatus: types.VerificationPassing,
			ClaimedCapabilities: []types.ClaimedOffer{{
				CapabilityID:    "rerank",
				OfferingID:      "zerank-2-default",
				InteractionMode: "http-reqresp@v0",
			}},
		}},
	}
}

func TestRunOnce_ApprovesOnlyPendingApprovable(t *testing.T) {
	r := newRepo(t)
	seedRerankOffer(t, r)

	approvable := approvableJoinRequest("a")
	notApprovable := approvableJoinRequest("b")
	notApprovable.RequestedBackends[0].VerificationStatus = types.VerificationFailing
	alreadyApproved := approvableJoinRequest("c")
	alreadyApproved.Status = types.JoinRequestApproved

	for _, jr := range []types.JoinRequest{approvable, notApprovable, alreadyApproved} {
		if err := r.PutJoinRequest(jr); err != nil {
			t.Fatalf("PutJoinRequest(%q) error = %v", jr.ID, err)
		}
	}

	summary, err := RunOnce(r, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Scanned != 2 {
		t.Fatalf("scanned = %d, want 2 (one already approved is skipped)", summary.Scanned)
	}
	if summary.Approved != 1 || len(summary.ApprovedIDs) != 1 || summary.ApprovedIDs[0] != "a" {
		t.Fatalf("approved = %d ids=%v, want [a]", summary.Approved, summary.ApprovedIDs)
	}
	if summary.NotApprovable != 1 || len(summary.NotApprovedIDs) != 1 || summary.NotApprovedIDs[0] != "b" {
		t.Fatalf("not_approvable = %d ids=%v, want [b]", summary.NotApprovable, summary.NotApprovedIDs)
	}

	got, err := r.GetJoinRequest("a")
	if err != nil {
		t.Fatalf("GetJoinRequest(a) error = %v", err)
	}
	if got.Status != types.JoinRequestApproved {
		t.Fatalf("status after RunOnce = %q, want approved", got.Status)
	}
	if got.ReviewReason != AutoApproveReason {
		t.Fatalf("review_reason = %q, want %q", got.ReviewReason, AutoApproveReason)
	}

	stillPending, err := r.GetJoinRequest("b")
	if err != nil {
		t.Fatalf("GetJoinRequest(b) error = %v", err)
	}
	if stillPending.Status != types.JoinRequestPending {
		t.Fatalf("non-approvable status = %q, want pending", stillPending.Status)
	}
}

func TestRunOnce_AuditEventEmitted(t *testing.T) {
	r := newRepo(t)
	seedRerankOffer(t, r)
	jr := approvableJoinRequest("a")
	if err := r.PutJoinRequest(jr); err != nil {
		t.Fatalf("PutJoinRequest() error = %v", err)
	}
	if _, err := RunOnce(r, time.Now().UTC()); err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	events, err := r.ListAuditEvents()
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.Kind == AuditEventKind && ev.ResourceID == "a" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("audit event %q for resource %q not found in %v", AuditEventKind, "a", events)
	}
}

func TestRunOnce_EmptyRepo(t *testing.T) {
	r := newRepo(t)
	summary, err := RunOnce(r, time.Now().UTC())
	if err != nil {
		t.Fatalf("RunOnce() error = %v", err)
	}
	if summary.Scanned != 0 || summary.Approved != 0 {
		t.Fatalf("empty repo summary = %+v, want zeros", summary)
	}
}
