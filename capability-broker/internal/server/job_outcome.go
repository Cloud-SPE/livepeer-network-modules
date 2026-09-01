package server

import (
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
)

// Reporting a dispatch outcome to the pool controller (plan 0045 §2).
//
// The controller's backend selection scores members on what the broker
// reports here, and the broker then pulls the resulting weights back
// through the pool snapshot. Until this existed the loop was open at
// this one leg: the reporter was built, stored, and never called, so
// every selection state sat at whatever seeded it.
//
// No policy lives here. The controller already decided what each
// outcome means — computeWindowSuccessScore counts only success and
// backend_failure, so a bad caller cannot degrade a member; the latency
// score includes caller_failure and drops backend_failure. The broker's
// only job is to classify honestly into those three buckets.

// jobOutcome maps the classification the idempotency layer already
// makes onto the controller's vocabulary.
func jobOutcome(status int) string {
	switch {
	case status < 400:
		return poolreport.OutcomeSuccess
	case status < 500:
		return poolreport.OutcomeCallerFailure
	default:
		return poolreport.OutcomeBackendFailure
	}
}

// reportJobOutcome sends one outcome for a dispatched exchange. It is
// best-effort and never blocks the response: the report is scoring
// input, not part of the exchange.
//
// Nothing is reported when the exchange never reached a runner — a
// nil dispatch means the broker refused it itself (payment, capability
// not served, protocol mismatch), and attributing that to a member's
// card would let the broker's decisions move a member's score.
//
// Nothing is reported for a runner with no member either. A standalone
// broker's own hardware — the orch's "pool of one" — has no member to
// attribute work to and no controller that would accept the report.
func (s *Server) reportJobOutcome(d *middleware.Dispatch, status int) {
	if d == nil || s.poolReporter == nil {
		return
	}
	sn, ok := s.runners.Get(d.HostID)
	if !ok {
		return
	}
	member := strings.TrimSpace(sn.Enrollment.MemberEthAddress)
	if member == "" {
		return
	}
	outcome := poolreport.BackendOutcome{
		BackendID:        d.BackendID,
		CapabilityID:     d.CapabilityID,
		OfferingID:       d.OfferingID,
		MemberEthAddress: member,
		Outcome:          jobOutcome(status),
		OccurredAt:       time.Now().UTC(),
	}
	// A runner that never answered has no first byte, and the controller
	// ignores a failure's latency anyway; leave it zero rather than
	// report the timeout as though it were a measurement.
	if d.Forwarded && !d.FirstByteAt.IsZero() && d.FirstByteAt.After(d.DispatchedAt) {
		outcome.LatencyMetricMS = d.FirstByteAt.Sub(d.DispatchedAt).Milliseconds()
	}
	poolreport.ReportBestEffort(s.poolReporter, outcome)
}

// splitBackendID recovers the host and local id from the broker's own
// backend id format, host|local (offer_dispatch.go syntheticCapability).
func splitBackendID(id string) (hostID, localID string) {
	host, local, _ := strings.Cut(id, "|")
	return host, local
}
