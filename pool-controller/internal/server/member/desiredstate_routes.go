package member

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/desiredstate"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// The agent's loop (plan 0044 §3.4): pull the desired state, make the
// host match it, report what happened. Both halves are authenticated by
// the enrollment bearer token, so a host can only ever see and report
// on itself.

type statusReport struct {
	Revision string          `json:"revision"`
	Services []serviceStatus `json:"services"`
}

type serviceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func registerDesiredStateRoutes(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/enrollments/{id}/desired-state", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		// A desired-state fetch IS the host checking in — it is the one
		// thing a running agent does on a schedule. Nothing else was
		// writing LastSeenAt, so every host read as never-seen and the
		// portal could not tell a dead agent from a healthy one.
		deps.touchEnrollment(enrollment, time.Now().UTC())
		doc, err := deps.desiredStateFor(enrollment)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// The revision doubles as an ETag: an agent that polls often
		// and changes rarely should mostly get 304 and no body.
		w.Header().Set("ETag", `"`+doc.Revision+`"`)
		if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, doc.Revision) {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		writeJSON(w, http.StatusOK, doc)
	})

	mux.HandleFunc("POST /member/v1/enrollments/{id}/status", func(w http.ResponseWriter, r *http.Request) {
		enrollment, ok := authorizeEnrollment(deps, r)
		if !ok {
			http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
			return
		}
		var report statusReport
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		now := time.Now().UTC()
		applied, err := deps.applyStatusReport(enrollment, report, now)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Status   string `json:"status"`
			Recorded int    `json:"recorded"`
		}{Status: "recorded", Recorded: applied})
	})
}

func (d Deps) desiredStateFor(enrollment types.HostEnrollment) (desiredstate.Document, error) {
	assignments := listEnrollmentAssignments(d.Repo, enrollment.ID)
	hardware, err := d.Repo.ListHardwareUnitsByEnrollment(enrollment.ID)
	if err != nil {
		return desiredstate.Document{}, err
	}
	return desiredstate.Build(desiredstate.Input{
		EnrollmentID: enrollment.ID,
		Assignments:  assignments,
		Hardware:     hardware,
		Catalog:      d.Catalog,
	})
}

// applyStatusReport records what the host actually managed to run.
//
// A service the agent reports as running moves its assignment out of
// pending — the pool asked for it and the host complied, which is the
// signal certification waits on. A service reported as stopped while
// draining is finished with, so the assignment retires: that is the
// only place a placement is removed, and it happens after the work has
// actually stopped rather than when the pool changed its mind.
func (d Deps) applyStatusReport(enrollment types.HostEnrollment, report statusReport, now time.Time) (int, error) {
	assignments := listEnrollmentAssignments(d.Repo, enrollment.ID)
	byService := make(map[string]types.TemplateAssignment, len(assignments))
	for _, assignment := range assignments {
		byService[desiredstate.ServiceName(assignment.ID)] = assignment
	}
	applied := 0
	for _, service := range report.Services {
		assignment, ok := byService[service.Name]
		if !ok {
			// A host reporting a service the pool never asked for is
			// not an error worth failing the whole report over; it is
			// stale local state the agent will remove on its next
			// reconcile.
			continue
		}
		next := assignment
		switch strings.ToLower(strings.TrimSpace(service.Status)) {
		case "running":
			if assignment.State == types.TemplateAssignmentPending {
				next.State = types.TemplateAssignmentTesting
			}
		case "stopped", "removed":
			// A draining service is retired only once its grace has
			// elapsed. The agent reports "stopped" as soon as it has
			// been told to drain — the container is still up and the
			// broker may still be finishing work it dispatched — so
			// retiring on that first report would withdraw the
			// placement while requests were in flight, which is the
			// thing draining exists to avoid.
			if assignment.State == types.TemplateAssignmentDraining && drainElapsed(assignment, now) {
				next.State = types.TemplateAssignmentRetired
			}
		case "failed":
			// The host tried and could not. Leave the state alone —
			// certification and the ladder decide what a failure means;
			// the report only records that it happened.
		default:
			continue
		}
		next.UpdatedAt = now
		if err := d.Repo.PutTemplateAssignment(next); err != nil {
			return applied, err
		}
		applied++
	}
	_ = d.Repo.AppendAuditEvent(types.AuditEvent{
		Kind:         "member_status_report",
		OccurredAt:   now,
		Actor:        enrollment.MemberEthAddress,
		ResourceID:   enrollment.ID,
		ResourceType: "host_enrollment",
		Details:      map[string]any{"revision": report.Revision, "services": len(report.Services)},
	})
	return applied, nil
}

// DrainGrace is how long a withdrawn placement keeps serving before it
// is retired. It bounds how long the broker has to finish work it had
// already dispatched; a request that outlives it was going to time out
// anyway.
const DrainGrace = 2 * time.Minute

func drainElapsed(assignment types.TemplateAssignment, now time.Time) bool {
	if assignment.DrainingSince.IsZero() {
		// No recorded start: treat the drain as complete rather than
		// stranding the placement forever on a missing timestamp.
		return true
	}
	return now.Sub(assignment.DrainingSince) >= DrainGrace
}

// touchEnrollment records that this host is alive. It is deliberately
// best-effort: failing a desired-state fetch because a liveness stamp
// could not be written would take a working host offline to record that
// it was working.
func (d Deps) touchEnrollment(enrollment types.HostEnrollment, now time.Time) {
	if d.Repo == nil {
		return
	}
	// Only when it has actually moved, so a fast poll does not rewrite
	// the row every few seconds.
	if now.Sub(enrollment.LastSeenAt) < time.Minute {
		return
	}
	enrollment.LastSeenAt = now
	enrollment.UpdatedAt = now
	_ = d.Repo.PutHostEnrollment(enrollment)
}
