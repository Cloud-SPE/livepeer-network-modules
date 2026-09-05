package repo

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Offers are no longer persisted — they are derived from the enabled
// template set on every read and every push — so the only control-plane
// entity this file still guards is the audit trail.
func TestStateRepoControlPlaneEntitiesPersist(t *testing.T) {
	repo, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = repo.Close() }()

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
