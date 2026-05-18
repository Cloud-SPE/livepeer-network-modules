package repo

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestStateRepoControlPlaneEntitiesPersist(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	offer := types.Offer{
		ID:              "offer-1",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit: config.WorkUnit{
			Name: "requests",
			Extractor: map[string]any{
				"type": "request-formula",
			},
		},
		Price: config.Price{AmountWei: "1", PerUnits: 1},
	}
	if err := repo.PutOffer(offer); err != nil {
		t.Fatalf("PutOffer() error = %v", err)
	}
	gotOffer, err := repo.GetOffer("offer-1")
	if err != nil {
		t.Fatalf("GetOffer() error = %v", err)
	}
	if gotOffer.Status != types.OfferStatusActive {
		t.Fatalf("offer status = %q, want %q", gotOffer.Status, types.OfferStatusActive)
	}

	member := types.MemberRecord{ID: "member-1", EthAddress: "0xabc", PayoutMode: "onchain"}
	if err := repo.PutMember(member); err != nil {
		t.Fatalf("PutMember() error = %v", err)
	}
	backend := types.MemberBackend{
		ID:        "backend-1",
		MemberID:  "member-1",
		Transport: "http",
		URL:       "http://backend",
		Auth:      config.AuthConfig{Method: "none"},
	}
	if err := repo.PutMemberBackend(backend); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}
	assignment := types.Assignment{ID: "assignment-1", OfferID: "offer-1", MemberBackendID: "backend-1"}
	if err := repo.PutAssignment(assignment); err != nil {
		t.Fatalf("PutAssignment() error = %v", err)
	}
	if err := repo.AppendAuditEvent(types.AuditEvent{ID: "audit-1", Kind: "offer_created"}); err != nil {
		t.Fatalf("AppendAuditEvent() error = %v", err)
	}

	backends, err := repo.ListMemberBackendsByMember("member-1")
	if err != nil {
		t.Fatalf("ListMemberBackendsByMember() error = %v", err)
	}
	if len(backends) != 1 || backends[0].ID != "backend-1" {
		t.Fatalf("backends = %#v", backends)
	}
	assignments, err := repo.ListAssignmentsByOffer("offer-1")
	if err != nil {
		t.Fatalf("ListAssignmentsByOffer() error = %v", err)
	}
	if len(assignments) != 1 || assignments[0].ID != "assignment-1" {
		t.Fatalf("assignments = %#v", assignments)
	}
	audits, err := repo.ListAuditEvents()
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(audits) != 1 || audits[0].ID != "audit-1" {
		t.Fatalf("audits = %#v", audits)
	}
}

func TestStateRepoJoinRequestAndVerificationTransitions(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	req := types.JoinRequest{
		ID:               "join-1",
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		RequestedBackends: []types.RequestedBackend{{
			ID:        "backend-1",
			Transport: "http",
			URL:       "http://backend",
		}},
	}
	if err := repo.PutJoinRequest(req); err != nil {
		t.Fatalf("PutJoinRequest() error = %v", err)
	}
	if err := repo.SetJoinRequestStatus("join-1", types.JoinRequestRejected, "no"); err != nil {
		t.Fatalf("SetJoinRequestStatus() error = %v", err)
	}
	gotReq, err := repo.GetJoinRequest("join-1")
	if err != nil {
		t.Fatalf("GetJoinRequest() error = %v", err)
	}
	if gotReq.Status != types.JoinRequestRejected || gotReq.ReviewedAt == nil {
		t.Fatalf("join request = %#v", gotReq)
	}

	backend := types.MemberBackend{ID: "backend-1", MemberID: "member-1", Transport: "http", URL: "http://backend"}
	if err := repo.PutMemberBackend(backend); err != nil {
		t.Fatalf("PutMemberBackend() error = %v", err)
	}
	verifiedAt := time.Now().UTC()
	if err := repo.SetVerificationResult("backend-1", types.VerificationPassing, "", verifiedAt); err != nil {
		t.Fatalf("SetVerificationResult() error = %v", err)
	}
	gotBackend, err := repo.GetMemberBackend("backend-1")
	if err != nil {
		t.Fatalf("GetMemberBackend() error = %v", err)
	}
	if gotBackend.VerificationStatus != types.VerificationPassing || gotBackend.LastVerifiedAt == nil {
		t.Fatalf("backend = %#v", gotBackend)
	}
}
