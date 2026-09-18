// Package claims releases GPU claims that never proved themselves.
//
// A card's identity is self-asserted: the member's agent reports a UUID
// and the pool has only their word that they hold it. Uniqueness of that
// UUID is what stops one card earning twice under two wallets, and it
// works — but it also means the first claim wins, so a UUID learned from
// a log or a previous owner can be used to lock the real owner out.
//
// That claim cannot EARN. A runner is pinned to its device, and a
// container pinned to hardware that is not present does not start, so a
// bogus claim never certifies and never sees paid work. All it can do is
// block. So the fix is not to verify possession up front — certification
// already does that, by making the card actually work — but to stop an
// unproven claim holding ground indefinitely.
//
// A claim that has never certified is released after a grace period. The
// block heals itself, and the only disputes that reach an operator are
// between two members who are BOTH running the card, which is the real
// fraud case and worth a person's time.
package claims

import (
	"fmt"
	"sort"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// DefaultGrace is how long a claim may go unproven.
//
// Long enough that a member who enrols on a Friday and installs on a
// Monday is never caught by it, short enough that a mistaken or
// malicious claim does not hold someone's card for a month.
const DefaultGrace = 7 * 24 * time.Hour

// Expired is one released claim.
type Expired struct {
	HardwareUnitID   string    `json:"hardware_unit_id"`
	GPUUUID          string    `json:"gpu_uuid"`
	MemberEthAddress string    `json:"member_eth_address"`
	ClaimedAt        time.Time `json:"claimed_at"`
	Reason           string    `json:"reason"`
}

// Summary is what one sweep did.
type Summary struct {
	Scanned int       `json:"scanned"`
	Expired []Expired `json:"expired"`
}

// Sweep releases unproven claims older than the grace period.
func Sweep(stateRepo *repo.StateRepo, grace time.Duration, now time.Time) (Summary, error) {
	var summary Summary
	if stateRepo == nil {
		return summary, fmt.Errorf("state repo is required")
	}
	if grace <= 0 {
		grace = DefaultGrace
	}
	units, err := stateRepo.ListHardwareUnits()
	if err != nil {
		return summary, err
	}
	proven, err := provenUnits(stateRepo)
	if err != nil {
		return summary, err
	}

	sort.Slice(units, func(i, j int) bool { return units[i].ID < units[j].ID })
	for _, unit := range units {
		if unit.State == types.HardwareUnitRetired {
			continue
		}
		summary.Scanned++
		if proven[unit.ID] {
			// It has done real work. Uniqueness protects it for as long
			// as it keeps the card, which is the whole point.
			continue
		}
		claimedAt := unit.CreatedAt
		if claimedAt.IsZero() {
			// No claim time recorded: treat it as new rather than
			// ancient, so a missing timestamp can never release a card
			// out from under someone.
			continue
		}
		if now.Sub(claimedAt) < grace {
			continue
		}
		unit.State = types.HardwareUnitRetired
		unit.UpdatedAt = now
		if err := stateRepo.PutHardwareUnit(unit); err != nil {
			return summary, fmt.Errorf("release %s: %w", unit.ID, err)
		}
		summary.Expired = append(summary.Expired, Expired{
			HardwareUnitID:   unit.ID,
			GPUUUID:          unit.GPUUUID,
			MemberEthAddress: unit.MemberEthAddress,
			ClaimedAt:        claimedAt,
			Reason:           "claimed but never certified within the grace period",
		})
		_ = stateRepo.AppendAuditEvent(types.AuditEvent{
			Kind:         "gpu_claim_expired",
			OccurredAt:   now,
			ResourceID:   unit.ID,
			ResourceType: "hardware_unit",
			Details: map[string]any{
				"gpu_uuid": unit.GPUUUID, "member_eth_address": unit.MemberEthAddress,
				"claimed_at": claimedAt, "grace": grace.String(),
			},
		})
	}
	return summary, nil
}

// provenUnits is the set of GPUs that have actually done the work.
//
// Proof is a passed certification run, not a state flag: a state can be
// set by anything, whereas a passed run means a container actually
// started on that device and served a request. That is the possession
// check, arrived at from the other end.
func provenUnits(stateRepo *repo.StateRepo) (map[string]bool, error) {
	assignments, err := stateRepo.ListTemplateAssignments()
	if err != nil {
		return nil, err
	}
	unitOf := make(map[string]string, len(assignments))
	proven := make(map[string]bool)
	for _, assignment := range assignments {
		unitOf[assignment.ID] = assignment.HardwareUnitID
		// A placement that reached probation or beyond got there by
		// passing certification, so it counts even if the run itself
		// has since been pruned.
		switch assignment.State {
		case types.TemplateAssignmentProbationary,
			types.TemplateAssignmentActive,
			types.TemplateAssignmentThrottled,
			types.TemplateAssignmentDraining:
			proven[assignment.HardwareUnitID] = true
		}
		if !assignment.LastCertifiedAt.IsZero() {
			proven[assignment.HardwareUnitID] = true
		}
	}
	runs, err := stateRepo.ListCertificationRuns()
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.Status != types.CertificationPassed {
			continue
		}
		if unitID := unitOf[run.AssignmentID]; unitID != "" {
			proven[unitID] = true
		}
	}
	return proven, nil
}
