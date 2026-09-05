package server

import (
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/offers"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolreport"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionengine"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

// Every close reason the engine can record has a deliberate place in the
// controller's vocabulary, and a reason this table does not know reports
// nothing rather than something.
func TestSessionOutcomeMapsEveryCloseReason(t *testing.T) {
	want := map[string]string{
		sessionengine.ReasonGatewayClose:         poolreport.OutcomeSuccess,
		sessionengine.ReasonRunnerEnded:          poolreport.OutcomeSuccess,
		sessionengine.ReasonRunnerFailed:         poolreport.OutcomeBackendFailure,
		sessionengine.ReasonRecoveryFailed:       poolreport.OutcomeBackendFailure,
		sessionengine.ReasonOpenFailed:           poolreport.OutcomeBackendFailure,
		sessionengine.ReasonHeartbeatLost:        poolreport.OutcomeCallerFailure,
		sessionengine.ReasonLeaseExpired:         poolreport.OutcomePolicyTermination,
		sessionengine.ReasonInsufficient:         poolreport.OutcomePaymentTermination,
		sessionengine.ReasonPaymentUnrecoverable: poolreport.OutcomePaymentTermination,
	}
	for reason, outcome := range want {
		if got := sessionOutcome(reason); got != outcome {
			t.Errorf("%s -> %q, want %q", reason, got, outcome)
		}
	}
	if got := sessionOutcome("something_new"); got != "" {
		t.Errorf("an unknown reason must not report, got %q", got)
	}
}

// The report carries the runner's identity in the job path's form, so
// the controller scores one runner as one runner whichever protocol it
// served — and nothing is reported for a session that was never pinned
// to an attached runner or whose runner has no member.
func TestSessionOutcomeIsReportedForPinnedSessions(t *testing.T) {
	ts, s := newJobOfferBrokerBare(t, nil, "")
	reporter := &capturingReporter{got: make(chan poolreport.BackendOutcome, 8)}
	s.poolReporter = reporter
	var status atomic.Int32
	status.Store(http.StatusOK)
	const member = "0x1111111111111111111111111111111111111111"
	attachOutcomeRunner(t, s, ts, member, &status)

	ended := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	rec := sessionstore.Record{
		Capability: "audio:transcribe.live", Offering: "nemo-meeting-live",
		BackendRef: sessionBackendRef("audio:transcribe.live", "nemo-meeting-live", offers.PairKey{HostID: "h1", LocalID: "chat"}),
		EndedAt:    ended,
	}
	s.reportSessionOutcome(rec, sessionengine.ReasonLeaseExpired)
	got := reporter.next(t)
	if got.Outcome != poolreport.OutcomePolicyTermination || got.BackendID != "h1|chat" ||
		got.CapabilityID != "audio:transcribe.live" || got.OfferingID != "nemo-meeting-live" ||
		got.MemberEthAddress != member || !got.OccurredAt.Equal(ended) || got.LatencyMetricMS != 0 {
		t.Fatalf("report = %+v", got)
	}

	// Unpinned: the broker's own backend, nobody to attribute to.
	rec.BackendRef = "audio:transcribe.live|nemo-meeting-live"
	s.reportSessionOutcome(rec, sessionengine.ReasonRunnerEnded)
	reporter.none(t)

	// Pinned to a host the broker does not know.
	rec.BackendRef = sessionBackendRef("audio:transcribe.live", "nemo-meeting-live", offers.PairKey{HostID: "ghost", LocalID: "x"})
	s.reportSessionOutcome(rec, sessionengine.ReasonRunnerEnded)
	reporter.none(t)
}
