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
		ID:           "offer-1",
		CapabilityID: "rerank",
		OfferingID:   "zerank-2-default",
		Protocol:     "paid-job/v1",
		WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:        config.Price{AmountWei: "1", PerUnits: 1},
	})
	if err != nil || offer.ID != "offer-1" {
		t.Fatalf("Create() offer=%#v err=%v", offer, err)
	}
	updated, err := Update(stateRepo, offer, Mutation{Status: string(types.OfferStatusDisabled)})
	if err != nil || updated.Status != types.OfferStatusDisabled {
		t.Fatalf("Update() updated=%#v err=%v", updated, err)
	}
	_, err = Create(stateRepo, Mutation{
		ID:           "offer-2",
		CapabilityID: "rerank",
		OfferingID:   "zerank-2-default",
		Protocol:     "paid-job/v1",
		WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:        config.Price{AmountWei: "1", PerUnits: 1},
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
		name          string
		capabilityID  string
		protocol      string
		wantSubstring string
	}{
		{
			name:          "video live rtmp capability",
			capabilityID:  "video:live.rtmp",
			protocol:      "paid-job/v1",
			wantSubstring: "video:live.rtmp",
		},
		{
			name:          "paid session protocol",
			capabilityID:  "video:transcode.abr",
			protocol:      "paid-session/v1",
			wantSubstring: "paid-session/v1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Create(stateRepo, Mutation{
				ID:           "offer-" + tc.name,
				CapabilityID: tc.capabilityID,
				OfferingID:   "any",
				Protocol:     tc.protocol,
				WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
				Price:        config.Price{AmountWei: "1", PerUnits: 1},
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

func TestCreateValidatesProtocolAndJobTransports(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	base := func(id string) Mutation {
		return Mutation{
			ID:           id,
			CapabilityID: "rerank",
			OfferingID:   id,
			Protocol:     types.ProtocolPaidJobV1,
			WorkUnit:     config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:        config.Price{AmountWei: "1", PerUnits: 1},
		}
	}

	accepted := base("offer-transports")
	accepted.Job = &types.OfferJobAxes{Transports: []string{"unary", "stream"}}
	offer, err := Create(stateRepo, accepted)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if offer.Job == nil || len(offer.Job.Transports) != 2 {
		t.Fatalf("stored job axes = %#v, want two transports", offer.Job)
	}

	badTransport := base("offer-bad-transport")
	badTransport.Job = &types.OfferJobAxes{Transports: []string{"webrtc"}}
	if _, err := Create(stateRepo, badTransport); err == nil || !strings.Contains(err.Error(), "unary|stream|multipart") {
		t.Fatalf("Create() err = %v, want transport rejection", err)
	}

	legacyMode := base("offer-legacy-mode")
	legacyMode.Protocol = "http-reqresp@v0"
	if _, err := Create(stateRepo, legacyMode); err == nil || !strings.Contains(err.Error(), "<name>/v<major>") {
		t.Fatalf("Create() err = %v, want protocol-tag rejection", err)
	}

	sessionAxes := base("offer-session-axes")
	sessionAxes.Protocol = types.ProtocolPaidSessionV1
	if _, err := Create(stateRepo, sessionAxes); err == nil {
		t.Fatalf("Create() err = nil, want paid-session rejection")
	}
}
