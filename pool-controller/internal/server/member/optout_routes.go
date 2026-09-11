package member

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// Per-template opt-out (plan 0044 §3.6 settings).
//
// A member may decline a template and may not request one. That
// asymmetry is deliberate: opting in would let the pool's scarce
// high-demand capacity go to whoever asked first rather than to the
// placement policy, but a member who does not want a given workload on
// their card is entitled to refuse, and placement honours it.
//
// Authorisation is the enrollment bearer token, and an opt-out is
// scoped to the address that token belongs to — a member cannot decline
// on someone else's behalf.

type optOutRequest struct {
	TemplateID     string `json:"template_id"`
	HardwareUnitID string `json:"hardware_unit_id,omitempty"`
	Reason         string `json:"reason,omitempty"`
}

func registerOptOutRoutes(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/enrollments/{id}/opt-outs", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		items, err := deps.Repo.ListMemberTemplateOptOutsFor(enrollment.MemberEthAddress)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			OptOuts []types.MemberTemplateOptOut `json:"opt_outs"`
		}{OptOuts: items})
	})

	mux.HandleFunc("POST /member/v1/enrollments/{id}/opt-outs", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		var req optOutRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		req.TemplateID = strings.TrimSpace(req.TemplateID)
		if req.TemplateID == "" {
			http.Error(w, "template_id is required", http.StatusBadRequest)
			return
		}
		// Declining a template this build does not ship is a typo, not
		// a preference: it would sit in the store shadowing nothing and
		// silently stop applying if a template of that id ever arrived.
		if deps.Catalog == nil {
			http.Error(w, "template catalog is not loaded", http.StatusInternalServerError)
			return
		}
		if _, known := deps.Catalog.Get(req.TemplateID); !known {
			http.Error(w, "no template "+req.TemplateID+" in the catalog", http.StatusNotFound)
			return
		}
		// A member may only scope an opt-out to their own hardware.
		if req.HardwareUnitID != "" {
			unit, err := deps.Repo.GetHardwareUnit(req.HardwareUnitID)
			if err != nil || !strings.EqualFold(strings.TrimSpace(unit.MemberEthAddress), strings.TrimSpace(enrollment.MemberEthAddress)) {
				http.Error(w, "hardware_unit_id is not one of yours", http.StatusNotFound)
				return
			}
		}
		now := time.Now().UTC()
		optOut := types.MemberTemplateOptOut{
			ID:               optOutID(enrollment.MemberEthAddress, req.TemplateID, req.HardwareUnitID),
			MemberEthAddress: enrollment.MemberEthAddress,
			TemplateID:       req.TemplateID,
			HardwareUnitID:   req.HardwareUnitID,
			Reason:           strings.TrimSpace(req.Reason),
			CreatedAt:        now,
		}
		if err := deps.Repo.PutMemberTemplateOptOut(optOut); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = deps.Repo.AppendAuditEvent(types.AuditEvent{
			Kind:         "member_template_opt_out",
			OccurredAt:   now,
			Actor:        enrollment.MemberEthAddress,
			ResourceID:   optOut.ID,
			ResourceType: "member_template_opt_out",
			Details:      map[string]any{"template_id": req.TemplateID, "hardware_unit_id": req.HardwareUnitID},
		})
		writeJSON(w, http.StatusOK, optOut)
	})

	mux.HandleFunc("DELETE /member/v1/enrollments/{id}/opt-outs/{optOutID}", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		id := strings.TrimSpace(r.PathValue("optOutID"))
		// Withdrawing someone else's refusal would place work on their
		// hardware, so ownership is checked before the delete.
		items, err := deps.Repo.ListMemberTemplateOptOutsFor(enrollment.MemberEthAddress)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		owned := false
		for _, item := range items {
			if item.ID == id {
				owned = true
				break
			}
		}
		if !owned {
			http.Error(w, "no such opt-out", http.StatusNotFound)
			return
		}
		if err := deps.Repo.DeleteMemberTemplateOptOut(id); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status string `json:"status"`
			ID     string `json:"id"`
		}{Status: "withdrawn", ID: id})
	})
}

// authorizeEnrollment resolves the bearer token to its enrollment.
func authorizeEnrollment(deps Deps, r *http.Request) (types.HostEnrollment, bool) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		return types.HostEnrollment{}, false
	}
	enrollment, _, ok := enrollmentFromBearer(deps.Enrollment, id, r)
	return enrollment, ok
}

// optOutID is derived rather than random so declining the same template
// twice is the same record: a member clicking twice should not leave two
// refusals to withdraw separately.
func optOutID(member, templateID, hardwareUnitID string) string {
	id := fmt.Sprintf("optout|%s|%s", strings.ToLower(strings.TrimSpace(member)), templateID)
	if hardwareUnitID != "" {
		id += "|" + hardwareUnitID
	}
	return id
}
