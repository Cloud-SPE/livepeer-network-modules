package server

import (
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/observability"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// Reporting a session's outcome to the pool controller (decision 10 of
// the 2026-09-02 walkthrough on lnm-of6).
//
// The job path has reported every dispatched exchange since plan 0045
// §2; the session path reported nothing, so a session runner's
// JobsServed stayed at zero and probation never ended — however many
// hours of stream it served. One outcome per session, at winddown,
// mapped from the engine's stable close reason onto the controller's
// vocabulary. A session is one sample however long it ran; the session
// templates' probation.min_jobs are set with that in mind.

// sessionOutcome maps a close reason onto the controller's vocabulary.
// Empty means "do not report": a reason this table does not know must
// not score a member by accident, so a new reason is a deliberate
// addition here, not a silent success or failure.
func sessionOutcome(reason string) string {
	switch reason {
	case sessionengine.ReasonGatewayClose, sessionengine.ReasonRunnerEnded:
		// The session did its work; whichever side ended it, it ended
		// cleanly.
		return poolreport.OutcomeSuccess
	case sessionengine.ReasonRunnerFailed, sessionengine.ReasonRecoveryFailed, sessionengine.ReasonOpenFailed:
		return poolreport.OutcomeBackendFailure
	case sessionengine.ReasonHeartbeatLost:
		// The gateway went away. Counts toward min_jobs, excluded from
		// the success score — the controller already treats a caller's
		// failure that way.
		return poolreport.OutcomeCallerFailure
	case sessionengine.ReasonLeaseExpired:
		return poolreport.OutcomePolicyTermination
	case sessionengine.ReasonInsufficient, sessionengine.ReasonPaymentUnrecoverable:
		return poolreport.OutcomePaymentTermination
	}
	return ""
}

// onSessionWinddown is the engine's terminal hook: metrics, then the
// report.
func (s *Server) onSessionWinddown(rec sessionstore.Record, reason string) {
	observability.RecordSessionWinddown(reason)
	s.reportSessionOutcome(rec, reason)
}

// reportSessionOutcome sends one outcome for an ended session. Same
// rules as reportJobOutcome: best-effort, never for a session that was
// not pinned to an attached runner (the broker's own hardware, or an
// open that failed before selection), never for a runner with no
// member to attribute to.
//
// No latency. A job has a first byte; a session's only comparable
// moment — the runner's create ack — is not timestamped on the record,
// and the controller's latency score stays neutral for a sample
// without one rather than reading zero as instant.
func (s *Server) reportSessionOutcome(rec sessionstore.Record, reason string) {
	if s.poolReporter == nil {
		return
	}
	outcome := sessionOutcome(reason)
	if outcome == "" {
		return
	}
	_, _, pair, pinned := splitSessionBackendRef(rec.BackendRef)
	if !pinned {
		return
	}
	sn, ok := s.runners.Get(pair.HostID)
	if !ok {
		return
	}
	member := strings.TrimSpace(sn.Enrollment.MemberEthAddress)
	if member == "" {
		return
	}
	occurred := rec.EndedAt
	if occurred.IsZero() {
		occurred = time.Now().UTC()
	}
	poolreport.ReportBestEffort(s.poolReporter, poolreport.BackendOutcome{
		// host|local, the same backend id the job path reports, so the
		// controller scores one runner as one runner whichever protocol
		// it served.
		BackendID:        pair.HostID + "|" + pair.LocalID,
		CapabilityID:     rec.Capability,
		OfferingID:       rec.Offering,
		MemberEthAddress: member,
		Outcome:          outcome,
		OccurredAt:       occurred,
	})
}
