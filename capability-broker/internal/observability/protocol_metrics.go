package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// v1 protocol-engine metrics. Labels are bounded vocabularies only —
// close reasons, transports, and coarse outcomes; never ids.
var (
	sessionOpensTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_protocol_session_opens_total",
		Help: "paid-session opens by outcome (opened|replayed|rejected|failed).",
	}, []string{"outcome"})

	sessionWinddownsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_protocol_session_winddowns_total",
		Help: "paid-session terminal winddowns by stable close reason.",
	}, []string{"reason"})

	sessionEventsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_protocol_session_events_total",
		Help: "runner events by outcome (accepted|duplicate|rejected|retryable|unauthorized).",
	}, []string{"outcome"})

	sessionDebitUnitsTotal = promauto.NewCounter(prometheus.CounterOpts{
		Name: "livepeer_protocol_session_debited_units_total",
		Help: "work units debited from paid-session usage claims.",
	})

	debitFailuresTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_protocol_debit_failures_total",
		Help: "debits that FAILED after work was delivered — every increment is unbilled work. Alert on any non-zero rate.",
	}, []string{"capability", "offering"})

	jobExchangesTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "livepeer_protocol_job_exchanges_total",
		Help: "paid-job exchanges by transport and outcome (ok|client_error|backend_error|replayed|refused).",
	}, []string{"transport", "outcome"})
)

// RecordSessionOpen counts one session-open outcome.
func RecordSessionOpen(outcome string) { sessionOpensTotal.WithLabelValues(outcome).Inc() }

// RecordSessionWinddown counts one terminal winddown by reason.
func RecordSessionWinddown(reason string) { sessionWinddownsTotal.WithLabelValues(reason).Inc() }

// RecordSessionEvent counts one runner-event outcome.
func RecordSessionEvent(outcome string) { sessionEventsTotal.WithLabelValues(outcome).Inc() }

// RecordDebitFailure counts a debit that failed after the work was
// already delivered. There is no recovering the work at that point, so
// the only thing left is to make the loss visible.
func RecordDebitFailure(capability, offering string) {
	debitFailuresTotal.WithLabelValues(capability, offering).Inc()
}

// RecordSessionDebit adds debited units from a usage claim.
func RecordSessionDebit(units uint64) { sessionDebitUnitsTotal.Add(float64(units)) }

// RecordJobExchange counts one paid-job exchange.
func RecordJobExchange(transport, outcome string) {
	jobExchangesTotal.WithLabelValues(transport, outcome).Inc()
}
