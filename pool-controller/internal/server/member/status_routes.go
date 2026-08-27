package member

import (
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The member API contract (plan 0044 §3.11): what a member can see and
// do about their own participation.
//
// Privacy is the rule that shapes all of it — a member sees only their
// own data, and never a figure that would let them infer another
// member's. That is why earnings are reported as this member's own
// amounts and not as a share of a pool total: a share plus a public
// total is another member's income by subtraction.

type hostStatusView struct {
	EnrollmentID string          `json:"enrollment_id"`
	HostLabel    string          `json:"host_label,omitempty"`
	Status       string          `json:"status"`
	LastSeenAt   time.Time       `json:"last_seen_at,omitempty"`
	GPUs         []gpuStatusView `json:"gpus"`
}

type gpuStatusView struct {
	HardwareUnitID string                `json:"hardware_unit_id"`
	GPUModel       string                `json:"gpu_model,omitempty"`
	State          string                `json:"state"`
	Placements     []placementStatusView `json:"placements"`
}

// placementStatusView carries the ladder state AND the reason for it.
// A member told only "throttled" has been given a verdict; one told why
// has been given something they can act on.
type placementStatusView struct {
	AssignmentID string    `json:"assignment_id"`
	TemplateID   string    `json:"template_id"`
	Role         string    `json:"role"`
	State        string    `json:"state"`
	ReasonCode   string    `json:"reason_code,omitempty"`
	Evidence     string    `json:"evidence,omitempty"`
	SinceAt      time.Time `json:"since_at,omitempty"`
	SharePPM     uint64    `json:"share_ppm,omitempty"`
}

func registerStatusRoutes(mux *http.ServeMux, deps Deps) {
	// A signed-in member's own hosts. Without this the portal has no
	// way to discover what to ask about: the agent holds the enrollment
	// token and the browser holds only a session.
	mux.HandleFunc("GET /member/v1/enrollments", func(w http.ResponseWriter, r *http.Request) {
		memberID, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "sign in first", http.StatusUnauthorized)
			return
		}
		member, err := deps.Repo.GetPoolMember(memberID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		all, err := deps.Repo.ListHostEnrollments()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mine := make([]types.HostEnrollment, 0, len(all))
		for _, enrollment := range all {
			if strings.EqualFold(strings.TrimSpace(enrollment.MemberEthAddress), strings.TrimSpace(member.EthAddress)) {
				// Redacted for the same reason the admin surface
				// redacts: the broker session credential is a live
				// secret and a read must never hand it back.
				mine = append(mine, redactEnrollment(enrollment))
			}
		}
		writeJSON(w, http.StatusOK, struct {
			Enrollments []types.HostEnrollment `json:"enrollments"`
		}{Enrollments: mine})
	})

	mux.HandleFunc("GET /member/v1/enrollments/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		view, err := deps.hostStatus(enrollment)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, view)
	})

	mux.HandleFunc("GET /member/v1/enrollments/{id}/earnings", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		earnings, err := deps.earningsFor(enrollment.MemberEthAddress)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, earnings)
	})

	// Rotating a credential issues a new one and revokes the old. The
	// member holds the only copy afterwards, which is why the new token
	// is returned exactly once and never readable again.
	mux.HandleFunc("POST /member/v1/enrollments/{id}/rotate", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		if deps.Enrollment == nil {
			http.Error(w, "enrollment service is not available", http.StatusInternalServerError)
			return
		}
		rotated, token, err := deps.Enrollment.Rotate(enrollment.ID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind: "member_credential_rotated", OccurredAt: time.Now().UTC(),
			Actor: enrollment.MemberEthAddress, ResourceID: enrollment.ID,
			ResourceType: "host_enrollment",
		})
		writeJSON(w, http.StatusOK, struct {
			Enrollment types.HostEnrollment `json:"enrollment"`
			Token      string               `json:"enrollment_token"`
			Note       string               `json:"note"`
		}{
			Enrollment: redactEnrollment(rotated), Token: token,
			Note: "store this now; it is not retrievable again",
		})
	})

	// Retiring is the member's own exit. It drains rather than deletes,
	// for the same reason the pool's own withdrawal does: work already
	// dispatched has to finish somewhere.
	mux.HandleFunc("POST /member/v1/enrollments/{id}/retire", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		now := time.Now().UTC()
		drained, err := deps.retireEnrollment(enrollment, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status   string `json:"status"`
			Draining int    `json:"draining_placements"`
			Note     string `json:"note"`
		}{
			Status: "retiring", Draining: drained,
			Note: "placements are draining; the host stops once in-flight work finishes",
		})
	})
}

func (d Deps) hostStatus(enrollment types.HostEnrollment) (hostStatusView, error) {
	view := hostStatusView{
		EnrollmentID: enrollment.ID,
		HostLabel:    enrollment.HostLabel,
		Status:       string(enrollment.Status),
		LastSeenAt:   enrollment.LastSeenAt,
		GPUs:         []gpuStatusView{},
	}
	units, err := d.Repo.ListHardwareUnitsByEnrollment(enrollment.ID)
	if err != nil {
		return view, err
	}
	assignments := listEnrollmentAssignments(d.Repo, enrollment.ID)
	reasons := d.latestLadderReasons(enrollment.MemberEthAddress)
	for _, unit := range units {
		gpu := gpuStatusView{
			HardwareUnitID: unit.ID, GPUModel: unit.GPUModel,
			State: string(unit.State), Placements: []placementStatusView{},
		}
		for _, assignment := range assignments {
			if assignment.HardwareUnitID != unit.ID {
				continue
			}
			placement := placementStatusView{
				AssignmentID: assignment.ID, TemplateID: assignment.TemplateID,
				Role: string(assignment.Role), State: string(assignment.State),
				SharePPM: assignment.ShareCapPPM, SinceAt: assignment.UpdatedAt,
			}
			if reason, ok := reasons[assignment.ID]; ok {
				placement.ReasonCode = reason.code
				placement.Evidence = reason.evidence
			}
			gpu.Placements = append(gpu.Placements, placement)
		}
		view.GPUs = append(view.GPUs, gpu)
	}
	return view, nil
}

type ladderReason struct{ code, evidence string }

// latestLadderReasons reads the most recent ladder transition per
// placement out of the audit trail, so the portal shows the same
// sentence the operator console does.
func (d Deps) latestLadderReasons(member string) map[string]ladderReason {
	out := map[string]ladderReason{}
	events, err := d.Repo.ListAuditEvents()
	if err != nil {
		return out
	}
	for _, event := range events {
		if event.Kind != "ladder_transition" || event.ResourceID == "" {
			continue
		}
		code, _ := event.Details["reason_code"].(string)
		evidence, _ := event.Details["evidence"].(string)
		// Events are listed oldest-first, so a later one overwrites an
		// earlier: the member sees the reason they are on now.
		out[event.ResourceID] = ladderReason{code: code, evidence: evidence}
	}
	return out
}

type earningsView struct {
	MemberEthAddress string          `json:"member_eth_address"`
	TotalPaidWei     string          `json:"total_paid_wei"`
	PendingWei       string          `json:"pending_wei"`
	Windows          []earningsEntry `json:"windows"`
}

type earningsEntry struct {
	SettlementWindowID string    `json:"settlement_window_id"`
	AmountWei          string    `json:"amount_wei"`
	Status             string    `json:"status"`
	At                 time.Time `json:"at,omitempty"`
}

// earningsFor reports this member's own amounts only.
func (d Deps) earningsFor(member string) (earningsView, error) {
	view := earningsView{MemberEthAddress: member, TotalPaidWei: "0", PendingWei: "0", Windows: []earningsEntry{}}
	batches, err := d.Repo.ListPayoutBatches()
	if err != nil {
		return view, err
	}
	paid := new(big.Int)
	pending := new(big.Int)
	want := strings.ToLower(strings.TrimSpace(member))
	for _, batch := range batches {
		for _, line := range batch.LineItems {
			if strings.ToLower(strings.TrimSpace(line.MemberEthAddress)) != want {
				continue
			}
			amount, ok := new(big.Int).SetString(strings.TrimSpace(line.AmountWei), 10)
			if !ok {
				continue
			}
			if batch.Status == types.PayoutBatchPaid {
				paid.Add(paid, amount)
			} else {
				pending.Add(pending, amount)
			}
			view.Windows = append(view.Windows, earningsEntry{
				SettlementWindowID: batch.SettlementWindowID,
				AmountWei:          line.AmountWei,
				Status:             string(batch.Status),
				At:                 batch.UpdatedAt,
			})
		}
	}
	view.TotalPaidWei = paid.String()
	view.PendingWei = pending.String()
	return view, nil
}

// retireEnrollment drains every placement on this host.
func (d Deps) retireEnrollment(enrollment types.HostEnrollment, now time.Time) (int, error) {
	assignments := listEnrollmentAssignments(d.Repo, enrollment.ID)
	drained := 0
	for _, assignment := range assignments {
		switch assignment.State {
		case types.TemplateAssignmentDraining, types.TemplateAssignmentRetired:
			continue
		}
		assignment.State = types.TemplateAssignmentDraining
		assignment.DrainingSince = now
		assignment.UpdatedAt = now
		if err := d.Repo.PutTemplateAssignment(assignment); err != nil {
			return drained, err
		}
		drained++
	}
	_ = d.Repo.AppendAuditEvent(types.AuditEvent{
		Kind: "member_host_retired", OccurredAt: now,
		Actor: enrollment.MemberEthAddress, ResourceID: enrollment.ID,
		ResourceType: "host_enrollment",
		Details:      map[string]any{"draining_placements": drained},
	})
	return drained, nil
}

// redactEnrollment withholds the broker session credential, for the
// same reason the admin surface does: it is a live secret and a read
// must never hand it back.
func redactEnrollment(in types.HostEnrollment) types.HostEnrollment {
	in.BrokerSessionCredential = ""
	return in
}
