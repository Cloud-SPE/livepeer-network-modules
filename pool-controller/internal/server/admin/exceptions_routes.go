package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/ladder"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The operator exception queue (plan 0044 §5 E9).
//
// Onboarding is zero-touch, which does not mean untouchable: what is
// left for a person is the set of things policy deliberately refuses to
// decide. Suspending a member, releasing a GPU claimed by two
// addresses, banning or retiring a host — each is a judgement about
// someone's participation, and none of them should happen because a
// score crossed a line.

type exceptionsView struct {
	GeneratedAt      time.Time            `json:"generated_at"`
	SuspendedMembers []types.PoolMember   `json:"suspended_members"`
	SuspendedGPUs    []types.HardwareUnit `json:"suspended_hardware"`
	HeldWindows      []heldWindowView     `json:"held_windows"`
	DuplicateGPUs    []duplicateGPUView   `json:"duplicate_gpus"`
}

type heldWindowView struct {
	WindowID string `json:"window_id"`
	Status   string `json:"status"`
	Anomaly  string `json:"anomaly,omitempty"`
	ScalePPM uint64 `json:"settlement_scale_ppm,omitempty"`
}

// duplicateGPUView is the cross-address GPU block (plan 0040 §4.2).
//
// It reads refused CLAIMS, not duplicate hardware rows. There are no
// duplicate rows to find: the uniqueness guard refuses the second write
// precisely so a challenger cannot take an incumbent's card contested
// by declaring its UUID. Grouping hardware units by uuid — which is
// what this did — could therefore only ever surface rows predating the
// guard, which is to say never.
type duplicateGPUView struct {
	ConflictID           string    `json:"conflict_id"`
	GPUUUID              string    `json:"gpu_uuid"`
	IncumbentEthAddress  string    `json:"incumbent_eth_address"`
	ChallengerEthAddress string    `json:"challenger_eth_address"`
	ChallengerHostID     string    `json:"challenger_host_id,omitempty"`
	Attempts             int       `json:"attempts"`
	FirstSeenAt          time.Time `json:"first_seen_at"`
	LastSeenAt           time.Time `json:"last_seen_at"`
}

type memberStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Actor  string `json:"actor,omitempty"`
}

func registerExceptionRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
	// Lifting a suspension (plan 0044 §3.5: any -> suspended ->
	// (operator) -> probationary).
	//
	// The default target is certification_testing, not probationary. A
	// placement is suspended for invalid output or repeated
	// certification failure — both of which are exactly what
	// certification tests — so sending it straight back to real, paid
	// traffic on an operator's say-so skips the cheap automated check
	// that would settle it. Re-proving costs one probe, and the ladder
	// promotes it from there on its own.
	//
	// An operator who knows certification was never the issue can ask
	// for probationary directly.
	mux.HandleFunc("POST /admin/v1/template-assignments/{id}/reinstate", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		assignment, err := deps.Repo.GetTemplateAssignment(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if assignment.State != types.TemplateAssignmentSuspended {
			http.Error(w, "only a suspended placement can be reinstated", http.StatusBadRequest)
			return
		}
		var req struct {
			To     string `json:"to"`
			Reason string `json:"reason"`
			Actor  string `json:"actor"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Reason) == "" {
			http.Error(w, "reason is required to reinstate a placement", http.StatusBadRequest)
			return
		}
		target := types.TemplateAssignmentTesting
		switch strings.TrimSpace(req.To) {
		case "", string(types.TemplateAssignmentTesting):
		case string(types.TemplateAssignmentProbationary):
			target = types.TemplateAssignmentProbationary
		default:
			http.Error(w, "to must be certification_testing or probationary_real_traffic", http.StatusBadRequest)
			return
		}
		// A placement whose member is suspended must not come back:
		// reinstating it would route work to someone the pool has
		// stopped dealing with.
		if member, err := deps.Repo.GetPoolMember(strings.ToLower(strings.TrimSpace(assignment.MemberEthAddress))); err == nil {
			if member.Status == types.MemberStatusSuspended {
				http.Error(w, "the member is suspended; reinstate them first", http.StatusConflict)
				return
			}
		}

		now := time.Now().UTC()
		assignment.State = target
		// The boundary the ladder counts failures from. Without it the
		// historical failures that caused the suspension would
		// re-suspend this placement on the next tick.
		assignment.ReinstatedAt = now
		assignment.UpdatedAt = now
		share, maxInFlight := deps.probationLimits()
		if target == types.TemplateAssignmentProbationary {
			// Both halves of probation, as the ladder's own entry to it
			// sets them: a share without the concurrency cap is a
			// runner that can still fall over under load, which is the
			// thing the cap exists to make cheap.
			assignment.ShareCapPPM = share
			assignment.MaxInFlight = maxInFlight
		} else {
			assignment.ShareCapPPM = 0
			assignment.MaxInFlight = 0
		}
		if err := deps.Repo.PutTemplateAssignment(assignment); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		deps.reinstateSelectionState(assignment, target, share)
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind: "placement_reinstated", OccurredAt: now,
			Actor: strings.TrimSpace(req.Actor), ResourceID: assignment.ID,
			ResourceType: "template_assignment",
			Details: map[string]any{
				"to": string(target), "reason": strings.TrimSpace(req.Reason),
				"member_eth_address": assignment.MemberEthAddress,
			},
		})
		writeAdminJSON(w, assignment, nil)
	}))

	// Resolving a contested GPU. The two outcomes are genuinely
	// different situations that look identical on the wire — a member
	// who sold their hardware and never retired the enrolment, versus
	// someone cloning a UUID to farm a second identity — which is why
	// this is an operator gesture and not a rule.
	mux.HandleFunc("POST /admin/v1/gpu-conflicts/{id}/{action}", auth(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		action := strings.TrimSpace(r.PathValue("action"))
		conflict, err := deps.Repo.GetHardwareClaimConflict(id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		var req struct {
			Reason string `json:"reason"`
			Actor  string `json:"actor"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Reason) == "" {
			// Either outcome takes a card away from someone. A decision
			// with no recorded cause cannot be reviewed, including by
			// the operator who made it.
			http.Error(w, "reason is required to resolve a GPU conflict", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		retired := 0
		switch action {
		case "transfer":
			// The incumbent really did hand the card over. Retire their
			// unit so the challenger's next attach is uncontested —
			// this does not bind the GPU to the challenger, it just
			// stops refusing them.
			retired = deps.retireIncumbentUnits(conflict, now)
			conflict.Resolution = types.ConflictTransferred
		case "reject":
			conflict.Resolution = types.ConflictRejected
		default:
			http.Error(w, "action must be transfer or reject", http.StatusBadRequest)
			return
		}
		conflict.ResolvedBy = strings.TrimSpace(req.Actor)
		conflict.ResolvedAt = now
		conflict.Reason = strings.TrimSpace(req.Reason)
		if err := deps.Repo.PutHardwareClaimConflict(conflict); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind: "gpu_conflict_resolved", OccurredAt: now,
			Actor: conflict.ResolvedBy, ResourceID: conflict.ID, ResourceType: "hardware_claim_conflict",
			Details: map[string]any{
				"gpu_uuid": conflict.GPUUUID, "resolution": string(conflict.Resolution),
				"incumbent": conflict.IncumbentEthAddress, "challenger": conflict.ChallengerEthAddress,
				"reason": conflict.Reason, "retired_units": retired,
			},
		})
		writeAdminJSON(w, struct {
			Conflict     types.HardwareClaimConflict `json:"conflict"`
			RetiredUnits int                         `json:"retired_units"`
		}{Conflict: conflict, RetiredUnits: retired}, nil)
	}))

	mux.HandleFunc("GET /admin/v1/exceptions", auth(func(w http.ResponseWriter, _ *http.Request) {
		view, err := deps.exceptions(time.Now().UTC())
		writeAdminJSON(w, view, err)
	}))

	// Suspending a member. Nothing could write MemberStatus after the
	// legacy model was deleted — the field persisted and no route set
	// it — so this is where the operator's judgement finally lands.
	mux.HandleFunc("PATCH /admin/v1/pool-members/{address}", auth(func(w http.ResponseWriter, r *http.Request) {
		address := strings.ToLower(strings.TrimSpace(r.PathValue("address")))
		member, err := deps.Repo.GetPoolMember(address)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		var req memberStatusRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		next := types.MemberStatus(strings.TrimSpace(req.Status))
		switch next {
		case types.MemberStatusActive, types.MemberStatusSuspended:
		default:
			http.Error(w, "status must be active or suspended", http.StatusBadRequest)
			return
		}
		// A suspension with no reason is a decision nobody can review
		// later, including the operator who made it.
		if next == types.MemberStatusSuspended && strings.TrimSpace(req.Reason) == "" {
			http.Error(w, "reason is required when suspending a member", http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		member.Status = next
		member.UpdatedAt = now
		if err := deps.Repo.PutPoolMember(member); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		drained := 0
		if next == types.MemberStatusSuspended {
			// Suspension has to reach the work, not just the record.
			// Draining rather than stopping, so in-flight requests
			// finish where they are.
			drained = deps.drainMemberPlacements(member.EthAddress, now)
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind: "member_status_changed", OccurredAt: now, Actor: strings.TrimSpace(req.Actor),
			// Lowercased, matching the id the member is looked up by:
			// recording the enrolled (possibly checksummed) spelling
			// would hide a member's status changes from anyone querying
			// the trail by their canonical address.
			ResourceID: strings.ToLower(member.EthAddress), ResourceType: "pool_member",
			Details: map[string]any{"status": string(next), "reason": req.Reason, "drained_placements": drained},
		})
		writeAdminJSON(w, struct {
			Member  types.PoolMember `json:"member"`
			Drained int              `json:"drained_placements"`
		}{Member: member, Drained: drained}, nil)
	}))
}

func (d Deps) exceptions(now time.Time) (exceptionsView, error) {
	view := exceptionsView{
		GeneratedAt:      now,
		SuspendedMembers: []types.PoolMember{},
		SuspendedGPUs:    []types.HardwareUnit{},
		HeldWindows:      []heldWindowView{},
		DuplicateGPUs:    []duplicateGPUView{},
	}
	members, err := d.Repo.ListPoolMembers()
	if err != nil {
		return view, err
	}
	for _, member := range members {
		if member.Status == types.MemberStatusSuspended {
			view.SuspendedMembers = append(view.SuspendedMembers, member)
		}
	}
	units, err := d.Repo.ListHardwareUnits()
	if err != nil {
		return view, err
	}
	for _, unit := range units {
		if unit.State == types.HardwareUnitSuspended {
			view.SuspendedGPUs = append(view.SuspendedGPUs, unit)
		}
	}
	conflicts, err := d.Repo.ListHardwareClaimConflicts()
	if err != nil {
		return view, err
	}
	for _, conflict := range conflicts {
		if !conflict.Open() {
			continue
		}
		view.DuplicateGPUs = append(view.DuplicateGPUs, duplicateGPUView{
			ConflictID:           conflict.ID,
			GPUUUID:              conflict.GPUUUID,
			IncumbentEthAddress:  conflict.IncumbentEthAddress,
			ChallengerEthAddress: conflict.ChallengerEthAddress,
			ChallengerHostID:     conflict.ChallengerHostID,
			Attempts:             conflict.Attempts,
			FirstSeenAt:          conflict.FirstSeenAt,
			LastSeenAt:           conflict.LastSeenAt,
		})
	}
	windows, err := d.Repo.ListSettlementWindows()
	if err != nil {
		return view, err
	}
	for _, window := range windows {
		// A window a person still has to look at: anything anomalous,
		// or anything sitting in pending_approval.
		if window.Anomaly == "" && window.Status != types.SettlementWindowPendingApproval {
			continue
		}
		view.HeldWindows = append(view.HeldWindows, heldWindowView{
			WindowID: window.ID, Status: string(window.Status),
			Anomaly: window.Anomaly, ScalePPM: window.SettlementScalePPM,
		})
	}
	return view, nil
}

func (d Deps) drainMemberPlacements(member string, now time.Time) int {
	assignments, err := d.Repo.ListTemplateAssignments()
	if err != nil {
		return 0
	}
	want := strings.ToLower(strings.TrimSpace(member))
	drained := 0
	for _, assignment := range assignments {
		if strings.ToLower(strings.TrimSpace(assignment.MemberEthAddress)) != want {
			continue
		}
		switch assignment.State {
		case types.TemplateAssignmentDraining, types.TemplateAssignmentRetired:
			continue
		}
		assignment.State = types.TemplateAssignmentDraining
		assignment.DrainingSince = now
		assignment.UpdatedAt = now
		if err := d.Repo.PutTemplateAssignment(assignment); err == nil {
			drained++
		}
	}
	return drained
}

// retireIncumbentUnits releases the contested GPU from its current
// holder. Retired rather than deleted: the card's history under that
// member is what makes a later dispute reviewable.
func (d Deps) retireIncumbentUnits(conflict types.HardwareClaimConflict, now time.Time) int {
	units, err := d.Repo.ListHardwareUnits()
	if err != nil {
		return 0
	}
	retired := 0
	for _, unit := range units {
		if strings.TrimSpace(unit.GPUUUID) != conflict.GPUUUID {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(unit.MemberEthAddress), conflict.IncumbentEthAddress) {
			continue
		}
		if unit.State == types.HardwareUnitRetired {
			continue
		}
		unit.State = types.HardwareUnitRetired
		unit.UpdatedAt = now
		if err := d.Repo.PutHardwareUnit(unit); err == nil {
			retired++
		}
	}
	return retired
}

// probationLimits is what a reinstated placement re-earns on.
func (d Deps) probationLimits() (uint64, int) {
	policy := ladder.DefaultPolicy
	if d.LadderPolicy != nil {
		policy = d.LadderPolicy().WithDefaults()
	}
	return policy.ProbationSharePPM, policy.ProbationMaxInFlight
}

// reinstateSelectionState puts the runner back in the draw.
//
// Both halves are required. Marking it eligible while leaving
// max_share_cap at zero would hand it UNLIMITED traffic, because the
// broker reads a zero cap as "no cap configured" — the same trap the
// ladder's own suspend path exists to avoid. A placement returning from
// suspension gets the probation share and re-earns from there.
func (d Deps) reinstateSelectionState(assignment types.TemplateAssignment, target types.TemplateAssignmentState, share uint64) {
	tmpl, ok := d.Catalog.Get(assignment.TemplateID)
	if !ok {
		return
	}
	// Seeded rather than skipped. Returning early on a missing row
	// would leave the ladder to create a fresh one on its next pass —
	// and a fresh row is eligible with MaxShareCap 0, which the broker
	// reads as UNCAPPED. That is the exact trap this function exists to
	// avoid, reached by the one path that looks like doing nothing.
	state, err := d.Repo.SeedBackendSelectionState(
		assignment.MemberEthAddress, ladder.BackendID(assignment), tmpl.Capability, tmpl.OfferingID)
	if err != nil {
		return
	}
	if target == types.TemplateAssignmentProbationary {
		state.State = types.BackendSelectionStateEligible
		state.ExclusionReason = ""
		state.MaxShareCap = float64(share) / 1_000_000
	} else {
		// Still excluded while it re-certifies: it has not proved
		// anything yet, and certification does not need traffic.
		state.State = types.BackendSelectionStateExcluded
		state.ExclusionReason = "awaiting_recertification"
		state.MaxShareCap = 0
	}
	_ = d.Repo.SaveBackendSelectionState(state)
}
