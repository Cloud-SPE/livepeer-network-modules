package legacyimport

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestBuildCollapsesIdenticalOffersIntoAssignments(t *testing.T) {
	cfg := &config.Config{
		Members: []config.Member{
			{
				EthAddress:  "0xaaa",
				DisplayName: "a",
				PayoutMode:  "onchain",
				Backends: []config.Backend{{
					ID:        "backend-a",
					Transport: "http",
					URL:       "http://a",
					Offerings: []config.Offering{{
						CapabilityID:    "rerank",
						OfferingID:      "zerank-2-default",
						InteractionMode: "http-reqresp@v0",
						WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
						Price:           config.Price{AmountWei: "1", PerUnits: 1},
					}},
				}},
			},
			{
				EthAddress:  "0xbbb",
				DisplayName: "b",
				PayoutMode:  "manual",
				Backends: []config.Backend{{
					ID:        "backend-b",
					Transport: "http",
					URL:       "http://b",
					Offerings: []config.Offering{{
						CapabilityID:    "rerank",
						OfferingID:      "zerank-2-default",
						InteractionMode: "http-reqresp@v0",
						WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
						Price:           config.Price{AmountWei: "1", PerUnits: 1},
					}},
				}},
			},
		},
	}

	built, err := Build(cfg, time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(built.Offers) != 1 {
		t.Fatalf("len(Offers) = %d, want 1", len(built.Offers))
	}
	if len(built.Assignments) != 2 {
		t.Fatalf("len(Assignments) = %d, want 2", len(built.Assignments))
	}
}

func TestPersistWritesEntitiesAndAuditEvent(t *testing.T) {
	state, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = state.Close() }()

	built := Result{
		Offers: []types.Offer{{
			ID:              "offer-1",
			CapabilityID:    "rerank",
			OfferingID:      "zerank-2-default",
			InteractionMode: "http-reqresp@v0",
			WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
			Price:           config.Price{AmountWei: "1", PerUnits: 1},
			Status:          types.OfferStatusActive,
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}},
		Members: []types.MemberRecord{{
			ID:         "member-1",
			EthAddress: "0xabc",
			PayoutMode: "onchain",
			Status:     types.MemberStatusActive,
			CreatedAt:  time.Now().UTC(),
			UpdatedAt:  time.Now().UTC(),
		}},
		Backends: []types.MemberBackend{{
			ID:                 "backend-1",
			MemberID:           "member-1",
			Transport:          "http",
			URL:                "http://backend",
			VerificationStatus: types.VerificationUnknown,
			Status:             types.BackendStatusActive,
			CreatedAt:          time.Now().UTC(),
			UpdatedAt:          time.Now().UTC(),
		}},
		Assignments: []types.Assignment{{
			ID:              "assignment-1",
			OfferID:         "offer-1",
			MemberBackendID: "backend-1",
			Status:          types.AssignmentStatusActive,
			CreatedAt:       time.Now().UTC(),
			UpdatedAt:       time.Now().UTC(),
		}},
	}

	if err := Persist(state, built, "tester", time.Now().UTC()); err != nil {
		t.Fatalf("Persist() error = %v", err)
	}
	offers, err := state.ListOffers()
	if err != nil {
		t.Fatalf("ListOffers() error = %v", err)
	}
	if len(offers) != 1 {
		t.Fatalf("offers = %#v", offers)
	}
	audits, err := state.ListAuditEvents()
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(audits) != 1 || audits[0].Kind != "legacy_import" {
		t.Fatalf("audits = %#v", audits)
	}
}
