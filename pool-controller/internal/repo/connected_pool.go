package repo

import (
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func (r *StateRepo) PutPoolMember(member types.PoolMember) error {
	now := time.Now().UTC()
	if member.CreatedAt.IsZero() {
		member.CreatedAt = now
	}
	member.UpdatedAt = now
	if member.ID == "" {
		member.ID = strings.ToLower(strings.TrimSpace(member.EthAddress))
	}
	if member.Status == "" {
		member.Status = types.MemberStatusActive
	}
	return putJSON(r, poolMembersBucket, member.ID, member)
}

func (r *StateRepo) GetPoolMember(id string) (types.PoolMember, error) {
	var out types.PoolMember
	err := getJSON(r, poolMembersBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListPoolMembers() ([]types.PoolMember, error) {
	return listJSON(r, poolMembersBucket, func(left, right types.PoolMember) bool {
		if left.EthAddress != right.EthAddress {
			return left.EthAddress < right.EthAddress
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) PutMemberNonce(nonce types.MemberNonce) error {
	now := time.Now().UTC()
	if nonce.CreatedAt.IsZero() {
		nonce.CreatedAt = now
	}
	return putJSON(r, memberNoncesBucket, nonce.ID, nonce)
}

func (r *StateRepo) GetMemberNonce(id string) (types.MemberNonce, error) {
	var out types.MemberNonce
	err := getJSON(r, memberNoncesBucket, id, &out)
	return out, err
}

func (r *StateRepo) MarkMemberNonceUsed(id string, usedAt time.Time) error {
	nonce, err := r.GetMemberNonce(id)
	if err != nil {
		return err
	}
	nonce.UsedAt = nowIfZero(usedAt)
	return putJSON(r, memberNoncesBucket, nonce.ID, nonce)
}

func (r *StateRepo) PutHostEnrollment(enrollment types.HostEnrollment) error {
	now := time.Now().UTC()
	if enrollment.CreatedAt.IsZero() {
		enrollment.CreatedAt = now
	}
	enrollment.UpdatedAt = now
	if enrollment.Status == "" {
		enrollment.Status = types.HostEnrollmentPending
	}
	return putJSON(r, hostEnrollmentsBucket, enrollment.ID, enrollment)
}

func (r *StateRepo) GetHostEnrollment(id string) (types.HostEnrollment, error) {
	var out types.HostEnrollment
	err := getJSON(r, hostEnrollmentsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListHostEnrollments() ([]types.HostEnrollment, error) {
	return listJSON(r, hostEnrollmentsBucket, func(left, right types.HostEnrollment) bool {
		if left.MemberEthAddress != right.MemberEthAddress {
			return left.MemberEthAddress < right.MemberEthAddress
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListHostEnrollmentsByMember(memberEthAddress string) ([]types.HostEnrollment, error) {
	items, err := r.ListHostEnrollments()
	if err != nil {
		return nil, err
	}
	memberEthAddress = strings.ToLower(strings.TrimSpace(memberEthAddress))
	out := make([]types.HostEnrollment, 0)
	for _, item := range items {
		if strings.ToLower(item.MemberEthAddress) == memberEthAddress {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) RevokeHostEnrollment(id string, reason string, revokedAt time.Time) (types.HostEnrollment, error) {
	enrollment, err := r.GetHostEnrollment(id)
	if err != nil {
		return types.HostEnrollment{}, err
	}
	enrollment.Status = types.HostEnrollmentRevoked
	enrollment.RevokedAt = nowIfZero(revokedAt)
	enrollment.RevocationReason = reason
	enrollment.UpdatedAt = enrollment.RevokedAt
	return enrollment, r.PutHostEnrollment(enrollment)
}

func (r *StateRepo) PutHardwareUnit(unit types.HardwareUnit) error {
	now := time.Now().UTC()
	if unit.CreatedAt.IsZero() {
		unit.CreatedAt = now
	}
	unit.UpdatedAt = now
	if unit.State == "" {
		unit.State = types.HardwareUnitRegistered
	}
	if err := r.ensureGPUUUIDAvailable(unit); err != nil {
		return err
	}
	return putJSON(r, hardwareUnitsBucket, unit.ID, unit)
}

func (r *StateRepo) ensureGPUUUIDAvailable(unit types.HardwareUnit) error {
	gpuUUID := strings.TrimSpace(unit.GPUUUID)
	if gpuUUID == "" {
		return fmt.Errorf("gpu_uuid is required")
	}
	items, err := r.ListHardwareUnits()
	if err != nil {
		return err
	}
	member := strings.ToLower(strings.TrimSpace(unit.MemberEthAddress))
	for _, existing := range items {
		if existing.ID == unit.ID {
			continue
		}
		if strings.TrimSpace(existing.GPUUUID) != gpuUUID {
			continue
		}
		if strings.ToLower(strings.TrimSpace(existing.MemberEthAddress)) != member {
			return fmt.Errorf("gpu_uuid %q is already bound to member %s", gpuUUID, existing.MemberEthAddress)
		}
	}
	return nil
}

func (r *StateRepo) GetHardwareUnit(id string) (types.HardwareUnit, error) {
	var out types.HardwareUnit
	err := getJSON(r, hardwareUnitsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListHardwareUnits() ([]types.HardwareUnit, error) {
	return listJSON(r, hardwareUnitsBucket, func(left, right types.HardwareUnit) bool {
		if left.MemberEthAddress != right.MemberEthAddress {
			return left.MemberEthAddress < right.MemberEthAddress
		}
		if left.EnrollmentID != right.EnrollmentID {
			return left.EnrollmentID < right.EnrollmentID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListHardwareUnitsByEnrollment(enrollmentID string) ([]types.HardwareUnit, error) {
	items, err := r.ListHardwareUnits()
	if err != nil {
		return nil, err
	}
	out := make([]types.HardwareUnit, 0)
	for _, item := range items {
		if item.EnrollmentID == enrollmentID {
			out = append(out, item)
		}
	}
	return out, nil
}

// Template overrides. The catalog itself is files on disk (see
// internal/templates); the database holds only what a pool decided
// about each one, so an operator's enable-and-price gesture is the only
// thing that has to survive a restart.
func (r *StateRepo) PutTemplateOverride(override types.TemplateOverride) error {
	override.UpdatedAt = time.Now().UTC()
	return putJSON(r, templateOverridesBucket, override.TemplateID, override)
}

func (r *StateRepo) GetTemplateOverride(templateID string) (types.TemplateOverride, error) {
	var out types.TemplateOverride
	err := getJSON(r, templateOverridesBucket, templateID, &out)
	return out, err
}

func (r *StateRepo) ListTemplateOverrides() ([]types.TemplateOverride, error) {
	return listJSON(r, templateOverridesBucket, func(left, right types.TemplateOverride) bool {
		return left.TemplateID < right.TemplateID
	})
}

// DeleteTemplateOverride returns the pool to the catalog's defaults for
// this template, which is not the same as disabling it.
func (r *StateRepo) DeleteTemplateOverride(templateID string) error {
	return deleteKey(r, templateOverridesBucket, templateID)
}

// Contested GPUs. The claim was refused; this is the record of it.
//
// RecordHardwareClaimConflict upserts on (gpu, challenger): a host that
// keeps re-attempting is one dispute seen repeatedly, not a queue of
// disputes, and an operator looking at the queue needs to see the
// former.
func (r *StateRepo) RecordHardwareClaimConflict(conflict types.HardwareClaimConflict) (types.HardwareClaimConflict, error) {
	if strings.TrimSpace(conflict.GPUUUID) == "" || strings.TrimSpace(conflict.ChallengerEthAddress) == "" {
		return types.HardwareClaimConflict{}, fmt.Errorf("gpu_uuid and challenger_eth_address are required")
	}
	conflict.GPUUUID = strings.TrimSpace(conflict.GPUUUID)
	conflict.ChallengerEthAddress = strings.ToLower(strings.TrimSpace(conflict.ChallengerEthAddress))
	conflict.IncumbentEthAddress = strings.ToLower(strings.TrimSpace(conflict.IncumbentEthAddress))
	conflict.ID = HardwareClaimConflictID(conflict.GPUUUID, conflict.ChallengerEthAddress)

	now := conflict.LastSeenAt
	if now.IsZero() {
		now = time.Now().UTC()
		conflict.LastSeenAt = now
	}
	if existing, err := r.GetHardwareClaimConflict(conflict.ID); err == nil {
		// A resolved dispute that recurs is reopened: the operator's
		// decision was about the claim they saw, and a host still
		// trying after a rejection is new information.
		conflict.FirstSeenAt = existing.FirstSeenAt
		conflict.Attempts = existing.Attempts + 1
		if !existing.Open() && existing.Resolution == types.ConflictRejected {
			conflict.Resolution = types.ConflictOpen
			conflict.Reason = "re-claimed after rejection"
		} else if !existing.Open() {
			conflict.Resolution = existing.Resolution
			conflict.ResolvedBy = existing.ResolvedBy
			conflict.ResolvedAt = existing.ResolvedAt
			conflict.Reason = existing.Reason
		}
	} else {
		conflict.FirstSeenAt = now
		conflict.Attempts = 1
	}
	if conflict.Resolution == "" {
		conflict.Resolution = types.ConflictOpen
	}
	if err := putJSON(r, hardwareConflictsBucket, conflict.ID, conflict); err != nil {
		return types.HardwareClaimConflict{}, err
	}
	return conflict, nil
}

func HardwareClaimConflictID(gpuUUID, challenger string) string {
	return strings.TrimSpace(gpuUUID) + "|" + strings.ToLower(strings.TrimSpace(challenger))
}

func (r *StateRepo) GetHardwareClaimConflict(id string) (types.HardwareClaimConflict, error) {
	var out types.HardwareClaimConflict
	err := getJSON(r, hardwareConflictsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListHardwareClaimConflicts() ([]types.HardwareClaimConflict, error) {
	return listJSON(r, hardwareConflictsBucket, func(left, right types.HardwareClaimConflict) bool {
		return left.ID < right.ID
	})
}

func (r *StateRepo) PutHardwareClaimConflict(conflict types.HardwareClaimConflict) error {
	return putJSON(r, hardwareConflictsBucket, conflict.ID, conflict)
}

// Member opt-outs. Only the member writes these, and only ever to
// decline: there is no opt-in, so an absent record means "the pool's
// policy applies", not "the member has not chosen yet".
func (r *StateRepo) PutMemberTemplateOptOut(optOut types.MemberTemplateOptOut) error {
	if optOut.CreatedAt.IsZero() {
		optOut.CreatedAt = time.Now().UTC()
	}
	return putJSON(r, memberOptOutsBucket, optOut.ID, optOut)
}

func (r *StateRepo) ListMemberTemplateOptOuts() ([]types.MemberTemplateOptOut, error) {
	return listJSON(r, memberOptOutsBucket, func(left, right types.MemberTemplateOptOut) bool {
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListMemberTemplateOptOutsFor(memberEthAddress string) ([]types.MemberTemplateOptOut, error) {
	items, err := r.ListMemberTemplateOptOuts()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(memberEthAddress))
	out := make([]types.MemberTemplateOptOut, 0, len(items))
	for _, item := range items {
		if strings.ToLower(strings.TrimSpace(item.MemberEthAddress)) == want {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) DeleteMemberTemplateOptOut(id string) error {
	return deleteKey(r, memberOptOutsBucket, id)
}

func (r *StateRepo) PutTemplateAssignment(assignment types.TemplateAssignment) error {
	now := time.Now().UTC()
	if assignment.CreatedAt.IsZero() {
		assignment.CreatedAt = now
	}
	assignment.UpdatedAt = now
	if assignment.Role == "" {
		assignment.Role = types.TemplateAssignmentPrimary
	}
	if assignment.State == "" {
		assignment.State = types.TemplateAssignmentPending
	}
	return putJSON(r, templateAssignmentsBucket, assignment.ID, assignment)
}

func (r *StateRepo) GetTemplateAssignment(id string) (types.TemplateAssignment, error) {
	var out types.TemplateAssignment
	err := getJSON(r, templateAssignmentsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListTemplateAssignments() ([]types.TemplateAssignment, error) {
	return listJSON(r, templateAssignmentsBucket, func(left, right types.TemplateAssignment) bool {
		if left.HardwareUnitID != right.HardwareUnitID {
			return left.HardwareUnitID < right.HardwareUnitID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) ListTemplateAssignmentsByHardwareUnit(hardwareUnitID string) ([]types.TemplateAssignment, error) {
	items, err := r.ListTemplateAssignments()
	if err != nil {
		return nil, err
	}
	out := make([]types.TemplateAssignment, 0)
	for _, item := range items {
		if item.HardwareUnitID == hardwareUnitID {
			out = append(out, item)
		}
	}
	return out, nil
}

func (r *StateRepo) PutCertificationRun(run types.CertificationRun) error {
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	run.UpdatedAt = now
	if run.Status == "" {
		run.Status = types.CertificationPending
	}
	return putJSON(r, certificationRunsBucket, run.ID, run)
}

func (r *StateRepo) ListCertificationRuns() ([]types.CertificationRun, error) {
	return listJSON(r, certificationRunsBucket, func(left, right types.CertificationRun) bool {
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) PutSettlementWindow(window types.SettlementWindow) error {
	now := time.Now().UTC()
	if window.CreatedAt.IsZero() {
		window.CreatedAt = now
	}
	window.UpdatedAt = now
	if window.Status == "" {
		window.Status = types.SettlementWindowOpen
	}
	return putJSON(r, settlementWindowsBucket, window.ID, window)
}

func (r *StateRepo) GetSettlementWindow(id string) (types.SettlementWindow, error) {
	var out types.SettlementWindow
	err := getJSON(r, settlementWindowsBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListSettlementWindows() ([]types.SettlementWindow, error) {
	return listJSON(r, settlementWindowsBucket, func(left, right types.SettlementWindow) bool {
		if left.StartRoundID != right.StartRoundID {
			return left.StartRoundID < right.StartRoundID
		}
		return left.ID < right.ID
	})
}

func (r *StateRepo) PutPayoutBatch(batch types.PayoutBatch) error {
	now := time.Now().UTC()
	if batch.CreatedAt.IsZero() {
		batch.CreatedAt = now
	}
	batch.UpdatedAt = now
	if batch.Status == "" {
		batch.Status = types.PayoutBatchPendingApproval
	}
	return putJSON(r, payoutBatchesBucket, batch.ID, batch)
}

func (r *StateRepo) GetPayoutBatch(id string) (types.PayoutBatch, error) {
	var out types.PayoutBatch
	err := getJSON(r, payoutBatchesBucket, id, &out)
	return out, err
}

func (r *StateRepo) ListPayoutBatches() ([]types.PayoutBatch, error) {
	return listJSON(r, payoutBatchesBucket, func(left, right types.PayoutBatch) bool {
		if !left.CreatedAt.Equal(right.CreatedAt) {
			return left.CreatedAt.After(right.CreatedAt)
		}
		return left.ID < right.ID
	})
}
