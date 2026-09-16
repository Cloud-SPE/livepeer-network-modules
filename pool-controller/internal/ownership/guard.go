package ownership

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Guard caches only grants validated during this process lifetime. Running
// assignments tolerate authority outages; restart never trusts a restored cache.
type Guard struct {
	repo      *repo.StateRepo
	client    *api.Client
	mu        sync.Mutex
	validated map[string]uint64
}

func NewGuard(store *repo.StateRepo, cfg api.Config) (*Guard, error) {
	if cfg.PoolID != "" && cfg.PoolID != store.PoolID() {
		return nil, fmt.Errorf("ownership pool_id differs from persisted controller identity")
	}
	cfg.PoolID = store.PoolID()
	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Guard{repo: store, client: client, validated: map[string]uint64{}}, nil
}
func (g *Guard) ListHardwareUnits() ([]types.HardwareUnit, error) { return g.repo.ListHardwareUnits() }
func (g *Guard) PutHardwareUnit(unit types.HardwareUnit) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	enrollment, err := g.repo.GetHostEnrollment(unit.EnrollmentID)
	if err != nil {
		return err
	}
	if len(enrollment.GPUUUIDs) > 0 {
		selected := false
		for _, device := range enrollment.GPUUUIDs {
			if strings.EqualFold(device, unit.GPUUUID) {
				selected = true
			}
		}
		if !selected {
			return fmt.Errorf("GPU is outside this enrollment's immutable selection")
		}
	}
	key := unit.EnrollmentID + "/" + strings.ToLower(unit.GPUUUID)
	if prior, err := g.repo.GetHardwareUnit(unit.ID); err == nil && prior.EnrollmentID == unit.EnrollmentID {
		unit.OwnershipGeneration = prior.OwnershipGeneration
	}
	if unit.OwnershipGeneration != 0 && g.validated[key] == unit.OwnershipGeneration {
		return g.repo.PutHardwareUnit(unit)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req := api.Request{DeviceID: unit.GPUUUID, EnrollmentID: unit.EnrollmentID, MemberWallet: unit.MemberEthAddress, ExpectedGeneration: unit.OwnershipGeneration}
	if req.ExpectedGeneration == 0 {
		current, err := g.client.Get(ctx, unit.GPUUUID)
		if err != nil {
			return err
		}
		if current.State == "released" && current.DestinationPoolID == g.repo.PoolID() {
			req.ExpectedGeneration = current.Generation
		}
	}
	grant, err := g.client.Claim(ctx, req)
	if err != nil {
		return err
	}
	unit.OwnershipGeneration = grant.Generation
	if err := g.repo.SaveHardwareOwnership(unit); err != nil {
		return err
	}
	g.validated[key] = grant.Generation
	return nil
}
func (g *Guard) VerifyExisting(units []types.HardwareUnit) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, unit := range units {
		if unit.State == types.HardwareUnitRetired || unit.State == types.HardwareUnitSuspended {
			continue
		}
		key := unit.EnrollmentID + "/" + strings.ToLower(unit.GPUUUID)
		if unit.OwnershipGeneration != 0 && g.validated[key] == unit.OwnershipGeneration {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		record, err := g.client.Get(ctx, unit.GPUUUID)
		cancel()
		if err != nil {
			return err
		}
		if unit.OwnershipGeneration == 0 || record.State != "active" || record.PoolID != g.repo.PoolID() || record.EnrollmentID != unit.EnrollmentID || record.Generation != unit.OwnershipGeneration {
			return fmt.Errorf("GPU ownership invalid; activation fenced")
		}
		g.validated[key] = record.Generation
	}
	return nil
}
