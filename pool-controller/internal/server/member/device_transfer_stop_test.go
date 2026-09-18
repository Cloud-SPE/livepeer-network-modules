package member

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestRegionalStopRequiresActualMatchingAgentReport(t *testing.T) {
	f := newDesiredStateFixture(t)
	unit, err := f.repo.GetHardwareUnit("unit-a")
	if err != nil {
		t.Fatal(err)
	}
	unit.OwnershipGeneration = 1
	if err := f.repo.SaveHardwareOwnership(unit); err != nil {
		t.Fatal(err)
	}
	assignment := f.putAssignment(t, "unit-a", f.host.ID, "chat-a", types.TemplateAssignmentActive)
	source := revenue.Source{PoolID: f.repo.PoolID(), SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "broker", ChainID: 42161, Payee: f.host.MemberEthAddress, URL: "https://broker.example"}
	item, err := f.repo.BeginDeviceTransfer(unit.ID, f.host.MemberEthAddress, "destination", "move GPU", []revenue.Source{source})
	if err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"draining", "revoking", "stopping"} {
		item.Phase = phase
		proof := ownership.DeviceDrainProof{PoolID: item.PoolID, SourceID: source.SourceID, BrokerID: source.BrokerID, EnrollmentID: item.EnrollmentID, DeviceID: item.DeviceID, Generation: item.Generation, StartedAt: time.Now().UTC(), ObservedAt: time.Now().UTC(), Revoked: true}
		item.Targets[0].Drain = proof
		item.Targets[0].Revocation = proof
		if err := f.repo.SaveDeviceTransfer(item); err != nil {
			t.Fatal(err)
		}
		item, err = f.repo.GetDeviceTransfer(item.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	mux := http.NewServeMux()
	Register(mux, Deps{Repo: f.repo, Catalog: f.catalog, VerifyOwnership: func([]types.HardwareUnit) error { return nil }})
	f.server.Close()
	f.server = httptest.NewServer(mux)
	defer f.server.Close()
	resp := f.get(t, f.host.ID, f.token, "")
	defer resp.Body.Close()
	var doc desiredstate.Document
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Services) != 1 || !doc.Services[0].Stop || !doc.Services[0].Draining {
		t.Fatalf("missing explicit stop %+v", doc)
	}
	stopped := statusReport{Revision: doc.Revision, Services: []serviceStatus{{Name: desiredstate.ServiceName(assignment.ID), Status: "stopped"}}}
	stale := stopped
	stale.Revision = "old-document"
	bad := f.postStatus(t, f.host.ID, f.token, stale)
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatal("stale stop accepted", bad.StatusCode)
	}
	other := f.postStatus(t, f.host.ID, f.otherTk, stopped)
	other.Body.Close()
	if other.StatusCode != 401 {
		t.Fatal("another enrollment stopped this GPU", other.StatusCode)
	}
	draining := stopped
	draining.Services = []serviceStatus{{Name: stopped.Services[0].Name, Status: "draining"}}
	pending := f.postStatus(t, f.host.ID, f.token, draining)
	pending.Body.Close()
	if pending.StatusCode != 200 {
		t.Fatal("drain progress rejected", pending.StatusCode)
	}
	item, _ = f.repo.GetDeviceTransfer(item.ID)
	if !item.StopConfirmedAt.IsZero() {
		t.Fatal("draining interpreted as actually stopped")
	}
	done := f.postStatus(t, f.host.ID, f.token, stopped)
	done.Body.Close()
	if done.StatusCode != 200 {
		t.Fatal("actual stop rejected", done.StatusCode)
	}
	item, _ = f.repo.GetDeviceTransfer(item.ID)
	if item.StopConfirmedAt.IsZero() || item.StopRevision != doc.Revision {
		t.Fatal("matching stop proof not persisted")
	}
	assignment, err = f.repo.GetTemplateAssignment(assignment.ID)
	if err != nil || assignment.State != types.TemplateAssignmentDraining {
		t.Fatal("agent bypassed ownership release", err)
	}
}
