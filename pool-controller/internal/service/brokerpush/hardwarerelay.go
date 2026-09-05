package brokerpush

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Hardware relay (plan 0043 item 17).
//
// Members no longer report GPU inventory to the controller. It rides the
// attach document, which the broker validates against the runner
// contract before anything else sees it, and the controller reads it
// back from the broker's runner view.
//
// This is strictly better than the report it replaces: the inventory the
// controller records is the same inventory the broker matched offers
// against, so the two cannot disagree. Under the old report they could —
// the controller believed a member's POST and the broker believed the
// backend it dialed.
//
// GPU-uniqueness (plan 0040 §4.2) still lives here, because it is a
// POOL rule: one broker cannot see that a GPU is already enrolled under
// a different ETH address elsewhere in the pool.

// Reader is the broker-side read surface the relay needs.
type Reader interface {
	Runners(ctx context.Context) ([]brokeradmin.RunnerView, error)
}

// HardwareStore is the controller state the relay writes.
type HardwareStore interface {
	ListHardwareUnits() ([]types.HardwareUnit, error)
	PutHardwareUnit(unit types.HardwareUnit) error
}

// RelayResult reports what one relay pass did.
type RelayResult struct {
	Hosts    int
	Upserted int
	// Conflicts are GPUs a different member already claims. They are
	// reported, never silently rebound: an override is an operator
	// gesture with an audit reason (plan 0040 §4.2).
	Conflicts []HardwareConflict
}

// HardwareConflict is one GPU claimed by two members.
type HardwareConflict struct {
	GPUUUID     string
	HostID      string
	ClaimedBy   string
	AlreadyHeld string
}

func (c HardwareConflict) String() string {
	return fmt.Sprintf("gpu %s reported by host %s (member %s) is already enrolled to member %s",
		c.GPUUUID, c.HostID, c.ClaimedBy, c.AlreadyHeld)
}

// RelayHardware reads the broker's runner view and records the hardware
// each connected host declared.
func RelayHardware(ctx context.Context, reader Reader, store HardwareStore, now time.Time) (RelayResult, error) {
	runners, err := reader.Runners(ctx)
	if err != nil {
		return RelayResult{}, fmt.Errorf("read runners: %w", err)
	}
	existing, err := store.ListHardwareUnits()
	if err != nil {
		return RelayResult{}, fmt.Errorf("list hardware: %w", err)
	}
	// Index the current owner of each GPU so a cross-member claim is
	// visible before it is written.
	ownerOf := make(map[string]types.HardwareUnit, len(existing))
	for _, u := range existing {
		if gpu := strings.TrimSpace(u.GPUUUID); gpu != "" {
			ownerOf[gpu] = u
		}
	}

	out := RelayResult{Hosts: len(runners)}
	for _, run := range runners {
		if run.State != "connected" {
			continue
		}
		member := strings.TrimSpace(run.Enrollment.MemberEthAddress)
		for _, hw := range run.Hardware {
			gpu := strings.TrimSpace(hw.GPUUUID)
			if gpu == "" {
				continue
			}
			prior, held := ownerOf[gpu]
			if held && member != "" && prior.MemberEthAddress != "" &&
				!strings.EqualFold(prior.MemberEthAddress, member) {
				out.Conflicts = append(out.Conflicts, HardwareConflict{
					GPUUUID: gpu, HostID: run.HostID, ClaimedBy: member, AlreadyHeld: prior.MemberEthAddress,
				})
				continue
			}
			unit := types.HardwareUnit{
				ID:               hardwareUnitID(gpu),
				EnrollmentID:     run.HostID,
				MemberEthAddress: member,
				GPUUUID:          gpu,
				GPUModel:         hw.GPUModel,
				VRAMBytes:        hw.VRAMBytes,
				DriverVersion:    hw.Driver,
				CUDAVersion:      hw.CUDA,
				RuntimeFacts:     hw.Facts,
				PublicURL:        strings.TrimSpace(run.PublicURL),
				Kind:             types.HardwareKind(hw.Kind),
				Cores:            hw.Cores,
				Threads:          hw.Threads,
				ISA:              hw.ISA,
				LastSeenAt:       now,
			}
			if held {
				// Keep the ladder state: re-reading inventory is not a
				// reason to demote a GPU that is already active.
				unit.State = prior.State
				unit.CreatedAt = prior.CreatedAt
			}
			if err := store.PutHardwareUnit(unit); err != nil {
				return out, fmt.Errorf("record hardware %s: %w", gpu, err)
			}
			ownerOf[gpu] = unit
			out.Upserted++
		}
	}
	return out, nil
}

// hardwareUnitID keys a unit by its GPU, which is the identity the pool
// actually enforces uniqueness on.
func hardwareUnitID(gpuUUID string) string {
	return "gpu-" + strings.ToLower(strings.NewReplacer(" ", "-", "_", "-", "/", "-", ":", "-").Replace(strings.TrimSpace(gpuUUID)))
}
