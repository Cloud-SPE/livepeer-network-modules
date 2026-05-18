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
