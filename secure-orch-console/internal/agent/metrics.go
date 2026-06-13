package agent

import (
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// Metrics is the agent's Prometheus surface (plan 0042 §9). The
// exposition format is hand-rolled deliberately: the cold signing
// host keeps its dependency surface minimal, and four metric
// families do not justify the client library. Served on the
// console's existing loopback listener — constraint #1 (no inbound
// connections) rules out a separate metrics listener; operators
// scrape through the same SSH tunnel the UI uses.
type Metrics struct {
	mu                 sync.Mutex
	polls              map[string]uint64
	decisions          map[string]uint64
	heldDepth          int
	lastPublishConfirm time.Time
	publishedExpiry    time.Time
	now                func() time.Time
}

// Poll result labels — pinned enums, cardinality-capped.
const (
	PollPulled      = "pulled"
	PollNotModified = "not_modified"
	PollNoCandidate = "no_candidate"
	PollError       = "error"
)

func NewMetrics() *Metrics {
	return &Metrics{polls: map[string]uint64{}, decisions: map[string]uint64{}, now: time.Now}
}

func (m *Metrics) IncPoll(result string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.polls[result]++
}

func (m *Metrics) IncDecision(action string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.decisions[action]++
}

func (m *Metrics) SetHeldDepth(depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.heldDepth = depth
}

func (m *Metrics) RecordPublishConfirm(at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastPublishConfirm = at
}

// SetPublishedExpiry records the published manifest's expires_at —
// the source of the page-the-operator gauge if the loop wedges.
func (m *Metrics) SetPublishedExpiry(expiry time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishedExpiry = expiry
}

// Handler serves the Prometheus text exposition format (v0.0.4).
func (m *Metrics) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

		fmt.Fprintf(w, "# HELP secure_orch_agent_polls_total Candidate poll outcomes.\n# TYPE secure_orch_agent_polls_total counter\n")
		for _, k := range sortedKeys(m.polls) {
			fmt.Fprintf(w, "secure_orch_agent_polls_total{result=%q} %d\n", k, m.polls[k])
		}
		fmt.Fprintf(w, "# HELP secure_orch_agent_decisions_total Classified-candidate dispositions.\n# TYPE secure_orch_agent_decisions_total counter\n")
		for _, k := range sortedKeys(m.decisions) {
			fmt.Fprintf(w, "secure_orch_agent_decisions_total{action=%q} %d\n", k, m.decisions[k])
		}
		fmt.Fprintf(w, "# HELP secure_orch_agent_held_queue_depth Held-for-operator candidates (0 or 1).\n# TYPE secure_orch_agent_held_queue_depth gauge\n")
		fmt.Fprintf(w, "secure_orch_agent_held_queue_depth %d\n", m.heldDepth)
		fmt.Fprintf(w, "# HELP secure_orch_agent_last_publish_confirm_timestamp_seconds Unix time of the last confirmed publish; 0 before the first.\n# TYPE secure_orch_agent_last_publish_confirm_timestamp_seconds gauge\n")
		confirm := float64(0)
		if !m.lastPublishConfirm.IsZero() {
			confirm = float64(m.lastPublishConfirm.Unix())
		}
		fmt.Fprintf(w, "secure_orch_agent_last_publish_confirm_timestamp_seconds %g\n", confirm)
		if !m.publishedExpiry.IsZero() {
			fmt.Fprintf(w, "# HELP secure_orch_agent_published_manifest_expiry_seconds Seconds until the published manifest expires (negative = expired). Page the operator on this.\n# TYPE secure_orch_agent_published_manifest_expiry_seconds gauge\n")
			fmt.Fprintf(w, "secure_orch_agent_published_manifest_expiry_seconds %g\n", m.publishedExpiry.Sub(m.now()).Seconds())
		}
	})
}

func sortedKeys(m map[string]uint64) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
