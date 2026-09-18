package repo

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	bolt "go.etcd.io/bbolt"
)

func guardDeviceTransferWrite(tx *bolt.Tx, bucket string, raw []byte) error {
	if bucket != hardwareUnitsBucket && bucket != templateAssignmentsBucket {
		return nil
	}
	transfers := tx.Bucket([]byte(deviceTransfersBucket))
	if transfers == nil {
		return nil
	}
	var unit types.HardwareUnit
	var assignment types.TemplateAssignment
	var unitID string
	if bucket == hardwareUnitsBucket {
		if err := json.Unmarshal(raw, &unit); err != nil {
			return err
		}
		unitID = unit.ID
	} else {
		if err := json.Unmarshal(raw, &assignment); err != nil {
			return err
		}
		unitID = assignment.HardwareUnitID
	}
	return transfers.ForEach(func(_, raw []byte) error {
		var transfer DeviceTransfer
		if err := json.Unmarshal(raw, &transfer); err != nil {
			return err
		}
		if transfer.HardwareUnitID != unitID {
			return nil
		}
		if bucket == hardwareUnitsBucket {
			expected := types.HardwareUnitSuspended
			if transfer.Phase == "released" {
				expected = types.HardwareUnitRetired
			}
			if unit.State != expected || unit.OwnershipGeneration != transfer.Generation || unit.EnrollmentID != transfer.EnrollmentID || !strings.EqualFold(unit.GPUUUID, transfer.DeviceID) || !strings.EqualFold(unit.MemberEthAddress, transfer.MemberWallet) {
				return fmt.Errorf("transferring device cannot be revived by an ordinary hardware write")
			}
		} else {
			known := false
			for _, id := range transfer.AssignmentIDs {
				if id == assignment.ID {
					known = true
				}
			}
			expected := types.TemplateAssignmentDraining
			if transfer.Phase == "released" {
				expected = types.TemplateAssignmentRetired
			}
			if !known || assignment.State != expected {
				return fmt.Errorf("transferring device placement is fenced")
			}
		}
		return nil
	})
}
func (r *StateRepo) TransferStopAssignments(host string) (map[string]bool, error) {
	items, err := r.DeviceTransfers()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for _, item := range items {
		if item.EnrollmentID == host && item.Phase == "stopping" {
			for _, id := range item.AssignmentIDs {
				out[id] = true
			}
		}
	}
	return out, nil
}
func (r *StateRepo) RecordTransferStopRevision(host, revision string, stoppedAssignments map[string]bool) error {
	if revision == "" {
		return fmt.Errorf("stop revision required")
	}
	return r.updateTransferStops(host, func(item *DeviceTransfer) error {
		if item.Phase != "stopping" || !item.StopConfirmedAt.IsZero() {
			return nil
		}
		for _, id := range item.AssignmentIDs {
			if !stoppedAssignments[id] {
				return fmt.Errorf("stop document is missing a transfer assignment")
			}
		}
		item.StopRevision = revision
		return nil
	})
}
func (r *StateRepo) ConfirmTransferStops(host, revision string, stoppedAssignments map[string]bool) error {
	return r.updateTransferStops(host, func(item *DeviceTransfer) error {
		if item.Phase != "stopping" || !item.StopConfirmedAt.IsZero() {
			return nil
		}
		if item.StopRevision == "" || item.StopRevision != revision {
			return nil
		}
		for _, id := range item.AssignmentIDs {
			if !stoppedAssignments[id] {
				return nil
			}
		}
		item.StopConfirmedAt = time.Now().UTC()
		return nil
	})
}
func (r *StateRepo) updateTransferStops(host string, change func(*DeviceTransfer) error) error {
	return r.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(deviceTransfersBucket))
		if b == nil {
			return nil
		}
		var updates []DeviceTransfer
		if err := b.ForEach(func(_, raw []byte) error {
			var item DeviceTransfer
			if err := json.Unmarshal(raw, &item); err != nil {
				return err
			}
			if item.EnrollmentID != host {
				return nil
			}
			before := item
			if err := change(&item); err != nil {
				return err
			}
			if !sameJSON(before, item) {
				item.Revision++
				item.UpdatedAt = time.Now().UTC()
				updates = append(updates, item)
			}
			return nil
		}); err != nil {
			return err
		}
		for _, item := range updates {
			if err := writeTransferJSON(tx, deviceTransfersBucket, item.ID, item); err != nil {
				return err
			}
		}
		return nil
	})
}
