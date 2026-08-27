package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokerpush"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

// fakeBroker is one member of the fleet: it records that it was reached
// and answers the two pushes, or fails them all.
type fakeBroker struct {
	server  *httptest.Server
	offers  atomic.Int32
	creds   atomic.Int32
	changed string
	revoked string
	fail    bool
}

func newFakeBroker(t *testing.T, changed, revoked string, fail bool) *fakeBroker {
	t.Helper()
	b := &fakeBroker{changed: changed, revoked: revoked, fail: fail}
	b.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/v1/offers":
			b.offers.Add(1)
			if b.fail {
				http.Error(w, "broker is down", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"applied":true,"changed":["` + b.changed + `"]}`))
		case "/admin/v1/credentials":
			b.creds.Add(1)
			if b.fail {
				http.Error(w, "broker is down", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write([]byte(`{"applied":true,"revoked_hosts":["` + b.revoked + `"]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(b.server.Close)
	return b
}

func fleetPushState() brokerpush.State {
	return brokerpush.State{
		Offers: []brokeradmin.OfferPush{{
			OfferingID: "shared", Capability: "openai:chat-completions", Protocol: "paid-job/v1",
			Price: brokeradmin.OfferPushPrice{AmountWei: "10", PerUnits: 1},
		}},
		Enrollments: []types.HostEnrollment{{
			ID: "host-1", BrokerSessionCredential: "s", Status: types.HostEnrollmentActive,
		}},
	}
}

// Brokers are separate machines. Letting the first unreachable one stop
// the loop would leave every broker after it serving a stale offer set
// for a reason that has nothing to do with them.
func TestPushToBrokerAttemptsEveryBrokerAfterAFailure(t *testing.T) {
	down := newFakeBroker(t, "", "", true)
	up := newFakeBroker(t, "shared", "host-9", false)
	alsoUp := newFakeBroker(t, "other", "host-8", false)

	cfg := &config.Config{Bootstrap: config.Bootstrap{Brokers: []config.Broker{
		{Name: "down", AdminURL: down.server.URL, TimeoutMS: 2000},
		{Name: "up", AdminURL: up.server.URL, TimeoutMS: 2000},
		{Name: "also-up", AdminURL: alsoUp.server.URL, TimeoutMS: 2000},
	}}}
	info := &types.DesiredBrokerRuntime{}
	err := (&runtimeState{}).pushToBroker(cfg, fleetPushState(), info)
	if err == nil {
		t.Fatal("pushToBroker() = nil; a broker that rejected the push must be reported")
	}
	// A partial push must not read like a total one.
	if !strings.Contains(err.Error(), "1 of 3") {
		t.Fatalf("error = %v, want it to name how many of how many failed", err)
	}
	if !strings.Contains(err.Error(), "down") {
		t.Fatalf("error = %v, want it to name the failing broker", err)
	}
	for _, b := range []struct {
		name   string
		broker *fakeBroker
	}{{"down", down}, {"up", up}, {"also-up", alsoUp}} {
		if b.broker.offers.Load() != 1 {
			t.Fatalf("%s received %d offer pushes, want 1", b.name, b.broker.offers.Load())
		}
	}
	// The failing broker never got past offers, so its credentials push
	// is the one that must be skipped — not the healthy brokers'.
	if down.creds.Load() != 0 {
		t.Fatalf("credentials were pushed to a broker that rejected the offers")
	}
	if up.creds.Load() != 1 || alsoUp.creds.Load() != 1 {
		t.Fatalf("healthy brokers received %d/%d credential pushes, want 1 each", up.creds.Load(), alsoUp.creds.Load())
	}
	// What the healthy half of the fleet reported still has to reach the
	// operator: a partial failure is not a reason to lose the result.
	if strings.Join(info.ChangedOffers, ",") != "other,shared" {
		t.Fatalf("ChangedOffers = %v, want the union across the fleet", info.ChangedOffers)
	}
	if strings.Join(info.RevokedHosts, ",") != "host-8,host-9" {
		t.Fatalf("RevokedHosts = %v, want the union across the fleet", info.RevokedHosts)
	}
}

// The same broker id reported by two brokers is one changed offer, not
// two: this is a fleet-wide view, not a per-broker log.
func TestPushToBrokerUnionsWhatTheFleetReported(t *testing.T) {
	first := newFakeBroker(t, "shared", "host-9", false)
	second := newFakeBroker(t, "shared", "host-9", false)
	cfg := &config.Config{Bootstrap: config.Bootstrap{Brokers: []config.Broker{
		{Name: "a", AdminURL: first.server.URL},
		{Name: "b", AdminURL: second.server.URL},
	}}}
	info := &types.DesiredBrokerRuntime{PushError: "a previous cycle failed"}
	if err := (&runtimeState{}).pushToBroker(cfg, fleetPushState(), info); err != nil {
		t.Fatalf("pushToBroker() error = %v", err)
	}
	if len(info.ChangedOffers) != 1 || info.ChangedOffers[0] != "shared" {
		t.Fatalf("ChangedOffers = %v, want one entry", info.ChangedOffers)
	}
	if len(info.RevokedHosts) != 1 || info.RevokedHosts[0] != "host-9" {
		t.Fatalf("RevokedHosts = %v, want one entry", info.RevokedHosts)
	}
	// A clean cycle has to clear the last one's error, or the recorded
	// runtime keeps accusing a broker that has since recovered.
	if info.PushError != "" {
		t.Fatalf("PushError = %q after a successful push", info.PushError)
	}
}

// A pool with no broker configured is a valid standalone deployment: it
// records the revision and pushes nowhere rather than erroring.
func TestPushToBrokerWithNoFleetIsNotAnError(t *testing.T) {
	info := &types.DesiredBrokerRuntime{}
	if err := (&runtimeState{}).pushToBroker(&config.Config{}, fleetPushState(), info); err != nil {
		t.Fatalf("pushToBroker() error = %v", err)
	}
	if len(info.ChangedOffers) != 0 || len(info.RevokedHosts) != 0 {
		t.Fatalf("a pool with no broker reported changes: %+v", info)
	}
}

// The legacy single-broker keys still reach a broker, so a dev
// deployment does not have to learn the list form.
func TestPushToBrokerUsesTheLegacySingleURL(t *testing.T) {
	only := newFakeBroker(t, "shared", "host-9", false)
	cfg := &config.Config{Bootstrap: config.Bootstrap{
		BrokerAdminURL:       only.server.URL,
		BrokerAdminTimeoutMS: 2000,
	}}
	info := &types.DesiredBrokerRuntime{}
	if err := (&runtimeState{}).pushToBroker(cfg, fleetPushState(), info); err != nil {
		t.Fatalf("pushToBroker() error = %v", err)
	}
	if only.offers.Load() != 1 || only.creds.Load() != 1 {
		t.Fatalf("legacy broker received offers=%d credentials=%d", only.offers.Load(), only.creds.Load())
	}
}
