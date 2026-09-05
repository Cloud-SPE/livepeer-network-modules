// Package health carries the broker's health vocabulary.
//
// It used to own a prober: the operator configured backend URLs and the
// broker polled them. Attached runners removed the need. A runner is
// reachable exactly when its attach tunnel is up, and it is fit to
// serve an offer exactly when certification says so — both facts the
// broker already holds, with no probe to schedule and no window in
// which a probe result is stale. What survives here is the vocabulary
// those facts are reported in, and the aggregation rules that turn a
// set of backend verdicts into one capability verdict.
package health

import "time"

type Status string

const (
	StatusReady       Status = "ready"
	StatusDraining    Status = "draining"
	StatusDegraded    Status = "degraded"
	StatusUnreachable Status = "unreachable"
	StatusStale       Status = "stale"
)

// Snapshot is one backend's verdict under one published tuple.
type Snapshot struct {
	ID                   string    `json:"id"`
	OfferingID           string    `json:"offering_id"`
	BackendID            string    `json:"backend_id,omitempty"`
	Status               Status    `json:"status"`
	Reason               string    `json:"reason,omitempty"`
	ProbeType            string    `json:"probe_type,omitempty"`
	ProbedAt             time.Time `json:"probed_at,omitempty"`
	StaleAfter           time.Time `json:"stale_after,omitempty"`
	ConsecutiveSuccesses int       `json:"consecutive_successes,omitempty"`
	ConsecutiveFailures  int       `json:"consecutive_failures,omitempty"`
}

// Response is the whole broker's verdict.
type Response struct {
	BrokerStatus string     `json:"broker_status"`
	GeneratedAt  time.Time  `json:"generated_at"`
	Capabilities []Snapshot `json:"capabilities"`
}

// AggregateStatuses reduces the backends under one published tuple to
// that tuple's status: ready if any backend can take work, draining if
// the only ones left are winding down, unreachable if none can be
// reached at all.
func AggregateStatuses(snaps []Snapshot) Status {
	if len(snaps) == 0 {
		return StatusUnreachable
	}
	best := StatusUnreachable
	for _, snap := range snaps {
		switch snap.Status {
		case StatusReady:
			return StatusReady
		case StatusDegraded:
			best = StatusDegraded
		case StatusDraining:
			if best != StatusDegraded {
				best = StatusDraining
			}
		case StatusStale:
			if best == StatusUnreachable {
				best = StatusStale
			}
		}
	}
	return best
}

// BrokerStatus reduces every tuple's verdict to the broker's own.
func BrokerStatus(caps []Snapshot) Status {
	grouped := map[string][]Snapshot{}
	for _, snap := range caps {
		key := snap.ID + "|" + snap.OfferingID
		grouped[key] = append(grouped[key], snap)
	}
	status := StatusReady
	for _, snaps := range grouped {
		switch AggregateStatuses(snaps) {
		case StatusUnreachable:
			status = StatusDegraded
		case StatusDegraded:
			if status == StatusReady {
				status = StatusDegraded
			}
		}
	}
	return status
}
