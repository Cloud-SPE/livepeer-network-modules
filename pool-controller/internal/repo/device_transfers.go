package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

const deviceTransfersBucket = "device_transfers"

type DeviceTransferTarget struct {
	Source     revenue.Source             `json:"source"`
	Drain      ownership.DeviceDrainProof `json:"drain"`
	Revocation ownership.DeviceDrainProof `json:"revocation"`
}
type DeviceTransfer struct {
	ID                string                 `json:"id"`
	PoolID            string                 `json:"pool_id"`
	HardwareUnitID    string                 `json:"hardware_unit_id"`
	EnrollmentID      string                 `json:"enrollment_id"`
	DeviceID          string                 `json:"device_id"`
	MemberWallet      string                 `json:"member_wallet"`
	Generation        uint64                 `json:"generation"`
	DestinationPoolID string                 `json:"destination_pool_id"`
	Actor             string                 `json:"actor"`
	Reason            string                 `json:"reason"`
	Phase             string                 `json:"phase"`
	Targets           []DeviceTransferTarget `json:"targets"`
	AssignmentIDs     []string               `json:"assignment_ids"`
	StopRevision      string                 `json:"stop_revision,omitempty"`
	StopConfirmedAt   time.Time              `json:"stop_confirmed_at,omitempty"`
	Revision          uint64                 `json:"revision"`
	LastError         string                 `json:"last_error,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

func (r *StateRepo) WithDeviceTransferLock(fn func() error) error {
	r.deviceTransferMu.Lock()
	defer r.deviceTransferMu.Unlock()
	return fn()
}
func writeTransferJSON(tx *bolt.Tx, bucket, key string, value any) error {
	b, err := tx.CreateBucketIfNotExists([]byte(bucket))
	if err != nil {
		return err
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if bucket == deviceTransfersBucket {
		var item DeviceTransfer
		if err := json.Unmarshal(raw, &item); err != nil {
			return err
		}
		var prior DeviceTransfer
		old := b.Get([]byte(key))
		if old != nil {
			if err := json.Unmarshal(old, &prior); err != nil {
				return err
			}
		}
		if old == nil || prior.Phase != item.Phase || prior.StopRevision != item.StopRevision || !prior.StopConfirmedAt.Equal(item.StopConfirmedAt) {
			audit, err := tx.CreateBucketIfNotExists([]byte("device_transfer_events"))
			if err != nil {
				return err
			}
			if err := audit.Put([]byte(fmt.Sprintf("%s/%020d", item.ID, item.Revision)), raw); err != nil {
				return err
			}
		}
	}
	return b.Put([]byte(key), raw)
}
func (r *StateRepo) BeginDeviceTransfer(unitID, wallet, destination, reason string, sources []revenue.Source) (DeviceTransfer, error) {
	var item DeviceTransfer
	if destination == "" || destination == r.PoolID() || strings.TrimSpace(reason) == "" || wallet == "" || len(sources) == 0 {
		return item, fmt.Errorf("transfer requires member, distinct destination, reason and source brokers")
	}
	err := r.db.Update(func(tx *bolt.Tx) error {
		var unit types.HardwareUnit
		if err := json.Unmarshal(tx.Bucket([]byte(hardwareUnitsBucket)).Get([]byte(unitID)), &unit); err != nil {
			return err
		}
		if !strings.EqualFold(unit.MemberEthAddress, wallet) || unit.OwnershipGeneration == 0 || unit.IsCPU() {
			return fmt.Errorf("member does not own managed GPU")
		}
		id := fmt.Sprintf("%s-%d", unit.ID, unit.OwnershipGeneration)
		b, err := tx.CreateBucketIfNotExists([]byte(deviceTransfersBucket))
		if err != nil {
			return err
		}
		if raw := b.Get([]byte(id)); raw != nil {
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			if item.DestinationPoolID != destination {
				return fmt.Errorf("transfer destination is immutable")
			}
			return nil
		}
		var enrollment types.HostEnrollment
		if err := json.Unmarshal(tx.Bucket([]byte(hostEnrollmentsBucket)).Get([]byte(unit.EnrollmentID)), &enrollment); err != nil {
			return err
		}
		if enrollment.DeviceOwnership[strings.ToLower(unit.GPUUUID)] != unit.OwnershipGeneration || unit.State == types.HardwareUnitRetired {
			return fmt.Errorf("source device grant unavailable")
		}
		now := time.Now().UTC()
		item = DeviceTransfer{ID: id, PoolID: r.PoolID(), HardwareUnitID: unit.ID, EnrollmentID: unit.EnrollmentID, DeviceID: strings.ToLower(unit.GPUUUID), MemberWallet: strings.ToLower(wallet), Generation: unit.OwnershipGeneration, DestinationPoolID: destination, Actor: wallet, Reason: reason, Phase: "requested", Revision: 1, CreatedAt: now, UpdatedAt: now}
		seen := map[string]bool{}
		for _, source := range sources {
			if source.PoolID != r.PoolID() || seen[source.SourceID] {
				return fmt.Errorf("invalid transfer broker set")
			}
			if err := source.Validate(); err != nil {
				return err
			}
			seen[source.SourceID] = true
			item.Targets = append(item.Targets, DeviceTransferTarget{Source: source})
		}
		unit.State = types.HardwareUnitSuspended
		unit.UpdatedAt = now
		if err := writeTransferJSON(tx, hardwareUnitsBucket, unit.ID, unit); err != nil {
			return err
		}
		var assignments []types.TemplateAssignment
		if err := tx.Bucket([]byte(templateAssignmentsBucket)).ForEach(func(_, raw []byte) error {
			var a types.TemplateAssignment
			if err := json.Unmarshal(raw, &a); err != nil {
				return err
			}
			if a.HardwareUnitID == unit.ID && a.State != types.TemplateAssignmentRetired {
				a.State = types.TemplateAssignmentDraining
				a.DrainingSince = now
				a.UpdatedAt = now
				assignments = append(assignments, a)
				item.AssignmentIDs = append(item.AssignmentIDs, a.ID)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, a := range assignments {
			if err := writeTransferJSON(tx, templateAssignmentsBucket, a.ID, a); err != nil {
				return err
			}
		}
		return writeTransferJSON(tx, deviceTransfersBucket, id, item)
	})
	return item, err
}
func (r *StateRepo) DeviceTransfers() ([]DeviceTransfer, error) {
	var items []DeviceTransfer
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(deviceTransfersBucket))
		if b == nil {
			return nil
		}
		return b.ForEach(func(_, raw []byte) error {
			var item DeviceTransfer
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			items = append(items, item)
			return nil
		})
	})
	return items, err
}
func (r *StateRepo) GetDeviceTransfer(id string) (DeviceTransfer, error) {
	var item DeviceTransfer
	err := getJSON(r, deviceTransfersBucket, id, &item)
	return item, err
}
func (r *StateRepo) SaveDeviceTransfer(item DeviceTransfer) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(deviceTransfersBucket))
		if b == nil {
			return fmt.Errorf("transfer missing")
		}
		var prior DeviceTransfer
		if err := json.Unmarshal(b.Get([]byte(item.ID)), &prior); err != nil {
			return err
		}
		a, binding := item, prior
		a.Phase = binding.Phase
		a.Targets = binding.Targets
		a.StopRevision = binding.StopRevision
		a.StopConfirmedAt = binding.StopConfirmedAt
		a.LastError = binding.LastError
		a.UpdatedAt = binding.UpdatedAt
		if !sameJSON(a, binding) {
			return fmt.Errorf("stale transfer revision or changed immutable identity")
		}
		if item.StopRevision != prior.StopRevision || !item.StopConfirmedAt.Equal(prior.StopConfirmedAt) {
			return fmt.Errorf("stop evidence requires member report")
		}
		phases := map[string]int{"requested": 0, "draining": 1, "revoking": 2, "stopping": 3, "released": 4}
		old, ok := phases[prior.Phase]
		next, known := phases[item.Phase]
		if !ok || !known || next < old || next > old+1 {
			return fmt.Errorf("invalid transfer phase transition")
		}
		if len(item.Targets) != len(prior.Targets) {
			return fmt.Errorf("transfer broker set is immutable")
		}
		for i, target := range item.Targets {
			if target.Source != prior.Targets[i].Source {
				return fmt.Errorf("transfer broker identity is immutable")
			}
			if next >= 2 && !deviceProofQuiet(target.Drain, item, target.Source, false) {
				return fmt.Errorf("all broker drain proofs required")
			}
			if next >= 3 && !deviceProofQuiet(target.Revocation, item, target.Source, true) {
				return fmt.Errorf("all broker revocation proofs required")
			}
		}
		if next >= 4 && item.StopConfirmedAt.IsZero() {
			return fmt.Errorf("agent stop proof required")
		}
		now := time.Now().UTC()
		if next >= 2 {
			var enrollment types.HostEnrollment
			if err := json.Unmarshal(tx.Bucket([]byte(hostEnrollmentsBucket)).Get([]byte(item.EnrollmentID)), &enrollment); err != nil {
				return err
			}
			delete(enrollment.DeviceOwnership, item.DeviceID)
			if err := writeTransferJSON(tx, hostEnrollmentsBucket, enrollment.ID, enrollment); err != nil {
				return err
			}
		}
		if next >= 4 {
			var unit types.HardwareUnit
			if err := json.Unmarshal(tx.Bucket([]byte(hardwareUnitsBucket)).Get([]byte(item.HardwareUnitID)), &unit); err != nil {
				return err
			}
			unit.State = types.HardwareUnitRetired
			unit.UpdatedAt = now
			if err := writeTransferJSON(tx, hardwareUnitsBucket, unit.ID, unit); err != nil {
				return err
			}
			for _, id := range item.AssignmentIDs {
				var a types.TemplateAssignment
				if err := json.Unmarshal(tx.Bucket([]byte(templateAssignmentsBucket)).Get([]byte(id)), &a); err != nil {
					return err
				}
				a.State = types.TemplateAssignmentRetired
				a.UpdatedAt = now
				if err := writeTransferJSON(tx, templateAssignmentsBucket, id, a); err != nil {
					return err
				}
			}
		}
		item.Revision++
		item.UpdatedAt = now
		return writeTransferJSON(tx, deviceTransfersBucket, item.ID, item)
	})
}
func deviceProofQuiet(p ownership.DeviceDrainProof, t DeviceTransfer, s revenue.Source, revoked bool) bool {
	return p.PoolID == t.PoolID && p.SourceID == s.SourceID && p.BrokerID == s.BrokerID && p.EnrollmentID == t.EnrollmentID && p.DeviceID == t.DeviceID && p.Generation == t.Generation && !p.StartedAt.IsZero() && !p.ObservedAt.IsZero() && p.PendingOperations+p.ActiveAuthorizations+p.UndeliveredReceipts+p.UnqualifiedWork == 0 && (!revoked || p.Revoked)
}

func (r *StateRepo) DeviceTransferHistory(id string) ([]DeviceTransfer, error) {
	var events []DeviceTransfer
	err := r.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("device_transfer_events"))
		if b == nil {
			return nil
		}
		return b.ForEach(func(key, raw []byte) error {
			if !strings.HasPrefix(string(key), id+"/") {
				return nil
			}
			var event DeviceTransfer
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			events = append(events, event)
			return nil
		})
	})
	return events, err
}
