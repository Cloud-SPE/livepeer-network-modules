package admin

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

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

// duplicateGPUView is the cross-address GPU block (plan 0040 §4.2). It
// is listed rather than resolved because the pool cannot tell which
// member is entitled to the card — only that two claim it.
type duplicateGPUView struct {
	GPUUUID string   `json:"gpu_uuid"`
	Members []string `json:"member_eth_addresses"`
}

type memberStatusRequest struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Actor  string `json:"actor,omitempty"`
}

func registerExceptionRoutes(mux *http.ServeMux, deps Deps, auth func(http.HandlerFunc) http.HandlerFunc) {
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
			ResourceID: member.EthAddress, ResourceType: "pool_member",
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
	byUUID := map[string]map[string]bool{}
	for _, unit := range units {
		if unit.State == types.HardwareUnitSuspended {
			view.SuspendedGPUs = append(view.SuspendedGPUs, unit)
		}
		if unit.GPUUUID == "" {
			continue
		}
		if byUUID[unit.GPUUUID] == nil {
			byUUID[unit.GPUUUID] = map[string]bool{}
		}
		byUUID[unit.GPUUUID][strings.ToLower(unit.MemberEthAddress)] = true
	}
	for uuid, addrs := range byUUID {
		if len(addrs) < 2 {
			continue
		}
		entry := duplicateGPUView{GPUUUID: uuid}
		for addr := range addrs {
			entry.Members = append(entry.Members, addr)
		}
		sortStrings(entry.Members)
		view.DuplicateGPUs = append(view.DuplicateGPUs, entry)
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

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j] < in[j-1]; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
