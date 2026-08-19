package assignmentpolicy

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestPreviewAndCreateAssignment(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	offer := types.Offer{
		ID:           "offer-1",
		CapabilityID: "rerank",
		OfferingID:   "zerank-2-default",
		Protocol:     "paid-job/v1",
		WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:        config.Price{AmountWei: "1", PerUnits: 1},
		Status:       types.OfferStatusActive,
	}
	_ = stateRepo.PutOffer(offer)
	_ = stateRepo.PutMember(types.MemberRecord{ID: "member-1", EthAddress: "0xabc", Status: types.MemberStatusActive, PayoutMode: "onchain"})
	_ = stateRepo.PutMemberBackend(types.MemberBackend{
		ID:                 "backend-1",
		MemberID:           "member-1",
		Transport:          "http",
		URL:                "http://backend",
		Status:             types.BackendStatusActive,
		VerificationStatus: types.VerificationPassing,
		ClaimedCapabilities: []types.ClaimedOffer{{
			CapabilityID: "rerank",
			OfferingID:   "zerank-2-default",
			Protocol:     "paid-job/v1",
		}},
	})

	view, err := Preview(stateRepo, "offer-1", "backend-1")
	if err != nil || !view.Compatible || view.MatchedClaim == nil {
		t.Fatalf("Preview() view=%#v err=%v", view, err)
	}
	_, err = CreateAssignment(stateRepo, types.Assignment{ID: "assignment-1", OfferID: "offer-1", MemberBackendID: "backend-1"})
	if err != nil {
		t.Fatalf("CreateAssignment() error = %v", err)
	}
	_, err = CreateAssignment(stateRepo, types.Assignment{ID: "assignment-2", OfferID: "offer-1", MemberBackendID: "backend-1"})
	if err == nil || !strings.Contains(err.Error(), "active assignment already exists") {
		t.Fatalf("duplicate CreateAssignment() err = %v", err)
	}
}
