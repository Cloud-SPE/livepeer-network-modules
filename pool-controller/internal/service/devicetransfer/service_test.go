package devicetransfer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type testAuthority struct {
	repo        *repo.OwnershipRepo
	loseReply   bool
	destination string
	wallet      string
	releases    int
}

func (a *testAuthority) Get(_ context.Context, device string) (ownership.Record, error) {
	return a.repo.Get(device)
}
func (a *testAuthority) Drain(_ context.Context, r ownership.Request) (ownership.Record, error) {
	return a.repo.Change("drain", "controller", r)
}
func (a *testAuthority) Release(_ context.Context, r ownership.Request) (ownership.Record, error) {
	a.releases++
	record, err := a.repo.Change("release", "controller", r)
	if err == nil && a.loseReply {
		a.loseReply = false
		_, err = a.repo.Change("claim", "destination-controller", ownership.Request{PoolID: a.destination, EnrollmentID: "destination-host", MemberWallet: a.wallet, DeviceID: r.DeviceID, ExpectedGeneration: r.ExpectedGeneration})
		if err != nil {
			return record, err
		}
		return record, errors.New("lost release response")
	}
	return record, err
}

type testBroker struct {
	source  revenue.Source
	offline bool
	active  uint64
	calls   []string
	began   time.Time
}

func (b *testBroker) DrainDevice(_ context.Context, r ownership.DeviceDrainRequest) (ownership.DeviceDrainProof, error) {
	b.calls = append(b.calls, r.Action)
	if b.offline {
		return ownership.DeviceDrainProof{}, errors.New("broker unavailable")
	}
	if b.began.IsZero() {
		b.began = time.Now().UTC()
	}
	p := ownership.DeviceDrainProof{PoolID: b.source.PoolID, SourceID: b.source.SourceID, BrokerID: b.source.BrokerID, EnrollmentID: r.EnrollmentID, DeviceID: r.DeviceID, Generation: r.Generation, Reason: r.Reason, StartedAt: b.began, ObservedAt: time.Now().UTC(), ActiveAuthorizations: b.active}
	if r.Action == "revoke" {
		if b.active != 0 {
			return p, errors.New("active work")
		}
		p.Revoked = true
	}
	return p, nil
}
func TestThreeBrokerTransferRestartStopRevisionAndLostReleaseReply(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "controller")
	st, err := repo.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	authorityRepo, err := repo.OpenOwnership(filepath.Join(dir, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	defer authorityRepo.Close()
	wallet := "0x" + strings.Repeat("1", 40)
	enrollment := types.HostEnrollment{ID: "host", MemberEthAddress: wallet, Status: types.HostEnrollmentActive, BrokerSessionCredential: "secret"}
	if err := st.PutHostEnrollment(enrollment); err != nil {
		t.Fatal(err)
	}
	for _, device := range []string{"gpu-a", "gpu-b"} {
		if _, err := authorityRepo.Change("claim", "controller", ownership.Request{DeviceID: device, PoolID: st.PoolID(), EnrollmentID: "host", MemberWallet: wallet}); err != nil {
			t.Fatal(err)
		}
		unit := types.HardwareUnit{ID: device, GPUUUID: device, EnrollmentID: "host", MemberEthAddress: wallet, OwnershipGeneration: 1, State: types.HardwareUnitActive}
		if err := st.SaveHardwareOwnership(unit); err != nil {
			t.Fatal(err)
		}
		if err := st.PutTemplateAssignment(types.TemplateAssignment{ID: "assignment-" + device, HardwareUnitID: device, HostEnrollmentID: "host", TemplateID: "template", State: types.TemplateAssignmentActive}); err != nil {
			t.Fatal(err)
		}
	}
	originalUnit, _ := st.GetHardwareUnit("gpu-a")
	originalEnrollment, _ := st.GetHostEnrollment("host")
	brokers := map[string]*testBroker{}
	for i := 1; i <= 3; i++ {
		source := revenue.Source{PoolID: st.PoolID(), SourceID: fmt.Sprintf("0x%064x", i), BrokerID: fmt.Sprintf("broker-%d", i), ChainID: 42161, Payee: wallet, URL: fmt.Sprintf("https://broker-%d.example", i)}
		if err := st.RegisterRevenueSource(source, 100, "initial topology"); err != nil {
			t.Fatal(err)
		}
		brokers[source.SourceID] = &testBroker{source: source}
	}
	authority := &testAuthority{repo: authorityRepo, destination: "pool_destination", wallet: wallet, loseReply: true}
	service := &Service{Repo: st, Authority: authority, Broker: func(s revenue.Source) (Broker, error) { return brokers[s.SourceID], nil }}
	first := brokers[fmt.Sprintf("0x%064x", 1)]
	second := brokers[fmt.Sprintf("0x%064x", 2)]
	first.active = 1
	second.offline = true
	item, err := service.Begin(ctx, "gpu-a", wallet, authority.destination, "move selected GPU")
	if err == nil || item.Phase != "draining" {
		t.Fatalf("partial drain advanced %+v %v", item, err)
	}
	if authority.releases != 0 {
		t.Fatal("release before drain")
	}
	sibling, _ := st.GetHardwareUnit("gpu-b")
	if sibling.State != types.HardwareUnitActive {
		t.Fatal("sibling suspended")
	}
	if err := st.PutHardwareUnit(originalUnit); err == nil {
		t.Fatal("stale heartbeat revived GPU")
	}
	if err := st.PutTemplateAssignment(types.TemplateAssignment{ID: "replacement", HardwareUnitID: "gpu-a", TemplateID: "template", State: types.TemplateAssignmentActive}); err == nil {
		t.Fatal("new placement bypassed transfer")
	}
	if _, err := service.Begin(ctx, "gpu-a", wallet, "another-destination", "changed"); err == nil {
		t.Fatal("destination changed")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = repo.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	service.Repo = st
	first.active = 0
	second.offline = false
	if err := service.Resume(ctx); err == nil || !strings.Contains(err.Error(), "stop report") {
		t.Fatalf("expected stop hold: %v", err)
	}
	item, err = st.GetDeviceTransfer(item.ID)
	if err != nil || item.Phase != "stopping" {
		t.Fatalf("phase %+v %v", item, err)
	}
	enrollment, err = st.GetHostEnrollment("host")
	if err != nil || enrollment.DeviceOwnership["gpu-a"] != 0 || enrollment.DeviceOwnership["gpu-b"] != 1 {
		t.Fatalf("device grants %+v %v", enrollment, err)
	}
	if err := st.PutHostEnrollment(originalEnrollment); err != nil {
		t.Fatal(err)
	}
	enrollment, _ = st.GetHostEnrollment("host")
	if enrollment.DeviceOwnership["gpu-a"] != 0 {
		t.Fatal("stale enrollment restored revoked grant")
	}
	stops := map[string]bool{"assignment-gpu-a": true}
	if err := st.RecordTransferStopRevision("host", "stop-revision", stops); err != nil {
		t.Fatal(err)
	}
	if err := st.ConfirmTransferStops("host", "old-revision", stops); err != nil {
		t.Fatal(err)
	}
	if err := service.Resume(ctx); err == nil || authority.releases != 0 {
		t.Fatal("stale stop released ownership")
	}
	if err := st.ConfirmTransferStops("host", "stop-revision", stops); err != nil {
		t.Fatal(err)
	}
	if err := service.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	item, _ = st.GetDeviceTransfer(item.ID)
	if item.Phase != "released" || item.StopConfirmedAt.IsZero() {
		t.Fatalf("release checkpoint %+v", item)
	}
	current, err := authorityRepo.Get("gpu-a")
	if err != nil || current.PoolID != authority.destination || current.Generation != 2 {
		t.Fatalf("destination ownership %+v %v", current, err)
	}
	if err := service.Resume(ctx); err != nil || authority.releases != 1 {
		t.Fatal("completed transfer repeated release")
	}
	final, _ := st.GetHardwareUnit("gpu-a")
	sibling, _ = st.GetHardwareUnit("gpu-b")
	if final.State != types.HardwareUnitRetired || sibling.State != types.HardwareUnitActive {
		t.Fatal("final hardware state wrong")
	}
}
