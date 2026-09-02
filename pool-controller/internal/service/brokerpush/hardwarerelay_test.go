package brokerpush

import (
	"context"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type fakeReader struct{ runners []brokeradmin.RunnerView }

func (f *fakeReader) Runners(context.Context) ([]brokeradmin.RunnerView, error) {
	return f.runners, nil
}

type memStore struct{ units map[string]types.HardwareUnit }

func newMemStore(seed ...types.HardwareUnit) *memStore {
	m := &memStore{units: map[string]types.HardwareUnit{}}
	for _, u := range seed {
		m.units[u.ID] = u
	}
	return m
}

func (m *memStore) ListHardwareUnits() ([]types.HardwareUnit, error) {
	out := make([]types.HardwareUnit, 0, len(m.units))
	for _, u := range m.units {
		out = append(out, u)
	}
	return out, nil
}

func (m *memStore) PutHardwareUnit(u types.HardwareUnit) error {
	m.units[u.ID] = u
	return nil
}

func runnerWithGPU(host, member, gpu string) brokeradmin.RunnerView {
	r := brokeradmin.RunnerView{HostID: host, State: "connected"}
	r.Enrollment.MemberEthAddress = member
	r.Hardware = append(r.Hardware, struct {
		GPUUUID   string            `json:"gpu_uuid"`
		GPUModel  string            `json:"gpu_model"`
		VRAMBytes uint64            `json:"vram_bytes"`
		Driver    string            `json:"driver,omitempty"`
		CUDA      string            `json:"cuda,omitempty"`
		Facts     map[string]string `json:"facts,omitempty"`
		Kind      string            `json:"kind,omitempty"`
		Cores     int               `json:"cores,omitempty"`
		Threads   int               `json:"threads,omitempty"`
		ISA       []string          `json:"isa,omitempty"`
	}{GPUUUID: gpu, GPUModel: "NVIDIA H100", VRAMBytes: 85899345920, Driver: "560.35.03"})
	return r
}

func TestRelayRecordsDeclaredHardware(t *testing.T) {
	now := time.Now().UTC()
	store := newMemStore()
	res, err := RelayHardware(context.Background(),
		&fakeReader{runners: []brokeradmin.RunnerView{runnerWithGPU("host-1", "0xaaa", "GPU-1")}}, store, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Upserted != 1 || len(res.Conflicts) != 0 {
		t.Fatalf("result = %+v", res)
	}
	units, _ := store.ListHardwareUnits()
	if len(units) != 1 || units[0].GPUUUID != "GPU-1" || units[0].EnrollmentID != "host-1" ||
		units[0].VRAMBytes != 85899345920 || units[0].DriverVersion != "560.35.03" {
		t.Fatalf("unit = %+v", units[0])
	}
}

// Re-reading inventory must not demote a GPU that already climbed the
// ladder.
func TestRelayPreservesLadderState(t *testing.T) {
	now := time.Now().UTC()
	seeded := types.HardwareUnit{
		ID: hardwareUnitID("GPU-1"), GPUUUID: "GPU-1", EnrollmentID: "host-1",
		MemberEthAddress: "0xaaa", State: types.HardwareUnitActive, CreatedAt: now.Add(-time.Hour),
	}
	store := newMemStore(seeded)
	if _, err := RelayHardware(context.Background(),
		&fakeReader{runners: []brokeradmin.RunnerView{runnerWithGPU("host-1", "0xaaa", "GPU-1")}}, store, now); err != nil {
		t.Fatal(err)
	}
	units, _ := store.ListHardwareUnits()
	if units[0].State != types.HardwareUnitActive {
		t.Fatalf("state = %q, want the ladder state preserved", units[0].State)
	}
	if !units[0].CreatedAt.Equal(seeded.CreatedAt) {
		t.Fatal("created_at was rewritten")
	}
}

// A GPU another member already holds is reported, never silently
// rebound: that is an operator gesture with an audit reason.
func TestRelayReportsCrossMemberConflict(t *testing.T) {
	now := time.Now().UTC()
	store := newMemStore(types.HardwareUnit{
		ID: hardwareUnitID("GPU-1"), GPUUUID: "GPU-1", EnrollmentID: "host-old",
		MemberEthAddress: "0xowner", State: types.HardwareUnitActive,
	})
	res, err := RelayHardware(context.Background(),
		&fakeReader{runners: []brokeradmin.RunnerView{runnerWithGPU("host-new", "0xthief", "GPU-1")}}, store, now)
	if err != nil {
		t.Fatal(err)
	}
	if res.Upserted != 0 || len(res.Conflicts) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Conflicts[0].AlreadyHeld != "0xowner" || res.Conflicts[0].ClaimedBy != "0xthief" {
		t.Fatalf("conflict = %+v", res.Conflicts[0])
	}
	units, _ := store.ListHardwareUnits()
	if units[0].EnrollmentID != "host-old" {
		t.Fatal("the conflicting claim overwrote the existing binding")
	}
}

// A disconnected host's inventory is not re-recorded: the relay reports
// what is attached now.
func TestRelaySkipsDisconnected(t *testing.T) {
	r := runnerWithGPU("host-1", "0xaaa", "GPU-1")
	r.State = "disconnected"
	store := newMemStore()
	res, err := RelayHardware(context.Background(), &fakeReader{runners: []brokeradmin.RunnerView{r}}, store, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Upserted != 0 {
		t.Fatalf("recorded a disconnected host: %+v", res)
	}
}
