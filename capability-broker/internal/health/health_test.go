package health

import "testing"

func snaps(statuses ...Status) []Snapshot {
	out := make([]Snapshot, 0, len(statuses))
	for _, st := range statuses {
		out = append(out, Snapshot{ID: "cap", OfferingID: "off", Status: st})
	}
	return out
}

// One backend that can take work is enough to call the tuple ready: a
// gateway routes to the tuple, not to a particular backend, so reporting
// degraded because a sibling is down would divert traffic that would
// have been served.
func TestAggregateStatusesPrefersAReadyBackend(t *testing.T) {
	for _, others := range []Status{StatusUnreachable, StatusDegraded, StatusDraining, StatusStale} {
		if got := AggregateStatuses(snaps(others, StatusReady)); got != StatusReady {
			t.Fatalf("AggregateStatuses(%s, ready) = %s, want ready", others, got)
		}
	}
}

// Draining is a decision, unreachable is a failure. A tuple whose only
// remaining backends are winding down should say so rather than claim to
// be broken — the operator asked for this.
func TestAggregateStatusesReportsDrainingOverUnreachable(t *testing.T) {
	if got := AggregateStatuses(snaps(StatusUnreachable, StatusDraining)); got != StatusDraining {
		t.Fatalf("AggregateStatuses(unreachable, draining) = %s, want draining", got)
	}
}

// A degraded backend still takes work, so it outranks draining, which
// has stopped taking new work by design.
func TestAggregateStatusesPrefersDegradedOverDraining(t *testing.T) {
	if got := AggregateStatuses(snaps(StatusDraining, StatusDegraded)); got != StatusDegraded {
		t.Fatalf("AggregateStatuses(draining, degraded) = %s, want degraded", got)
	}
}

// An advertised offer with nothing behind it is unreachable. Reporting
// ready for an empty set would advertise capacity that does not exist.
func TestAggregateStatusesTreatsNoBackendsAsUnreachable(t *testing.T) {
	if got := AggregateStatuses(nil); got != StatusUnreachable {
		t.Fatalf("AggregateStatuses(nil) = %s, want unreachable", got)
	}
}

// The broker is only ready when every tuple it advertises is servable.
// One dead tuple degrades the broker, because a resolver that believes
// the broker is ready will keep sending it work it cannot do.
func TestBrokerStatusDegradesOnAnyUnservableTuple(t *testing.T) {
	ready := Snapshot{ID: "a", OfferingID: "1", Status: StatusReady}
	dead := Snapshot{ID: "b", OfferingID: "1", Status: StatusUnreachable}
	if got := BrokerStatus([]Snapshot{ready}); got != StatusReady {
		t.Fatalf("BrokerStatus(ready) = %s, want ready", got)
	}
	if got := BrokerStatus([]Snapshot{ready, dead}); got != StatusDegraded {
		t.Fatalf("BrokerStatus(ready, unreachable) = %s, want degraded", got)
	}
}

// Backends are grouped per tuple before aggregating, so one tuple's
// failure cannot be masked by another tuple's healthy backend.
func TestBrokerStatusGroupsByTuple(t *testing.T) {
	in := []Snapshot{
		{ID: "a", OfferingID: "1", BackendID: "h|x", Status: StatusReady},
		{ID: "b", OfferingID: "1", BackendID: "h|y", Status: StatusUnreachable},
		{ID: "b", OfferingID: "1", BackendID: "h|z", Status: StatusUnreachable},
	}
	if got := BrokerStatus(in); got != StatusDegraded {
		t.Fatalf("BrokerStatus = %s, want degraded", got)
	}
}
