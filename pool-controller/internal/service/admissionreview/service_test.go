package admissionreview

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestBuildJoinRequestPreviewAndApprove(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	offer := types.Offer{
		ID:              "offer-1",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
		Status:          types.OfferStatusActive,
	}
	if err := stateRepo.PutOffer(offer); err != nil {
		t.Fatalf("PutOffer() error = %v", err)
	}

	req := types.JoinRequest{
		ID:               "join-1",
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		Status:           types.JoinRequestPending,
		RequestedBackends: []types.RequestedBackend{{
			ID:                 "backend-1",
			Transport:          "http",
			URL:                "http://backend",
			VerificationStatus: types.VerificationPassing,
			ClaimedCapabilities: []types.ClaimedOffer{{
				CapabilityID:    "rerank",
				OfferingID:      "zerank-2-default",
				InteractionMode: "http-reqresp@v0",
			}},
		}},
	}
	if err := stateRepo.PutJoinRequest(req); err != nil {
		t.Fatalf("PutJoinRequest() error = %v", err)
	}

	preview := BuildJoinRequestPreview(req, []types.Offer{offer})
	if !preview.Approavable || len(preview.BackendPreviews) != 1 || !preview.BackendPreviews[0].Servable {
		t.Fatalf("preview = %#v", preview)
	}

	preview, err = ApproveJoinRequest(stateRepo, req, "ok", time.Now().UTC())
	if err != nil {
		t.Fatalf("ApproveJoinRequest() error = %v", err)
	}
	if !preview.Approavable {
		t.Fatalf("preview.Approavable = false")
	}
	gotReq, err := stateRepo.GetJoinRequest("join-1")
	if err != nil {
		t.Fatalf("GetJoinRequest() error = %v", err)
	}
	if gotReq.Status != types.JoinRequestApproved {
		t.Fatalf("join request status = %q", gotReq.Status)
	}
	members, err := stateRepo.ListMembers()
	if err != nil || len(members) != 1 {
		t.Fatalf("ListMembers() members=%d err=%v", len(members), err)
	}
}

func TestBuildJoinClaimPreviewFlagsOutOfPoolScope(t *testing.T) {
	view := BuildJoinClaimPreview(types.ClaimedOffer{
		CapabilityID:    "video:live.rtmp",
		OfferingID:      "live-default",
		InteractionMode: "rtmp-ingress-hls-egress@v0",
	}, nil)
	if view.Servable {
		t.Fatalf("expected non-servable preview, got %#v", view)
	}
	joined := strings.Join(view.Reasons, "; ")
	if !strings.Contains(joined, "video:live.rtmp") {
		t.Fatalf("preview.Reasons missing capability reference: %q", joined)
	}
	if !strings.Contains(joined, "0032") {
		t.Fatalf("preview.Reasons missing 0032 cite: %q", joined)
	}
}
