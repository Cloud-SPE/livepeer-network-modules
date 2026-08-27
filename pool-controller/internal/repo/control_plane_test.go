package repo

import (
	"testing"

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
		ID:           "offer-1",
		CapabilityID: "rerank",
		OfferingID:   "zerank-2-default",
		Protocol:     "paid-job/v1",
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

	if err := repo.AppendAuditEvent(types.AuditEvent{ID: "audit-1", Kind: "offer_created"}); err != nil {
		t.Fatalf("AppendAuditEvent() error = %v", err)
	}

	audits, err := repo.ListAuditEvents()
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(audits) != 1 || audits[0].ID != "audit-1" {
		t.Fatalf("audits = %#v", audits)
	}
}

func TestListAuditEventsFiltered(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

	_ = repo.AppendAuditEvent(types.AuditEvent{ID: "a1", Kind: "offer_created", ResourceType: "offer", ResourceID: "offer-1"})
	_ = repo.AppendAuditEvent(types.AuditEvent{ID: "a2", Kind: "offer_updated", ResourceType: "offer", ResourceID: "offer-1"})
	_ = repo.AppendAuditEvent(types.AuditEvent{ID: "a3", Kind: "template_assignment_created", ResourceType: "template_assignment", ResourceID: "assign-1"})

	items, err := repo.ListAuditEventsFiltered("offer_updated", "offer", "offer-1", 10)
	if err != nil {
		t.Fatalf("ListAuditEventsFiltered() error = %v", err)
	}
	if len(items) != 1 || items[0].ID != "a2" {
		t.Fatalf("items = %#v", items)
	}
}
