package offerservice

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestCreateAndUpdateOffer(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	offer, err := Create(stateRepo, Mutation{
		ID:              "offer-1",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
	})
	if err != nil || offer.ID != "offer-1" {
		t.Fatalf("Create() offer=%#v err=%v", offer, err)
	}
	updated, err := Update(stateRepo, offer, Mutation{Status: string(types.OfferStatusDisabled)})
	if err != nil || updated.Status != types.OfferStatusDisabled {
		t.Fatalf("Update() updated=%#v err=%v", updated, err)
	}
	_, err = Create(stateRepo, Mutation{
		ID:              "offer-2",
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
	})
	if err == nil || !strings.Contains(err.Error(), "conflicts with existing offer") {
		t.Fatalf("duplicate Create() err = %v", err)
	}
}

func TestCreateRejectsOutOfPoolScopeOffers(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	cases := []struct {
		name            string
		capabilityID    string
		interactionMode string
		wantSubstring   string
	}{
		{
			name:            "video live rtmp capability",
			capabilityID:    "video:live.rtmp",
			interactionMode: "http-reqresp@v0",
			wantSubstring:   "video:live.rtmp",
		},
		{
			name:            "rtmp ingress hls egress interaction mode",
			capabilityID:    "video:transcode.abr",
			interactionMode: "rtmp-ingress-hls-egress@v0",
			wantSubstring:   "rtmp-ingress-hls-egress@v0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Create(stateRepo, Mutation{
				ID:              "offer-" + tc.name,
				CapabilityID:    tc.capabilityID,
				OfferingID:      "any",
				InteractionMode: tc.interactionMode,
				WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
				Price:           config.Price{AmountWei: "1", PerUnits: 1},
			})
			if err == nil {
				t.Fatalf("Create() expected rejection, got nil error")
			}
			if !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("Create() err = %q, missing %q", err.Error(), tc.wantSubstring)
			}
			if !strings.Contains(err.Error(), "0032") {
				t.Fatalf("Create() err = %q, expected reference to plan 0032", err.Error())
			}
		})
	}
}
