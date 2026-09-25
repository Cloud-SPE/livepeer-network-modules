package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
)

func TestConcurrentSessionOpensCountStartingAndReplayAtCapacity(t *testing.T) {
	var fixture struct {
		MaxInFlight    int    `json:"max_in_flight"`
		QueueLimit     int    `json:"queue_limit"`
		Opens          int    `json:"concurrent_opens"`
		Accepted       int    `json:"accepted"`
		OpenStatus     int    `json:"open_status"`
		ReplayStatus   int    `json:"replay_status"`
		RejectedStatus int    `json:"rejected_status"`
		Error          string `json:"rejected_error"`
		Backoff        string `json:"backoff_seconds"`
	}
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/capacity-ownership.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var armed atomic.Bool
	var creates atomic.Int32
	entered := make(chan struct{}, 10)
	proceed := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(proceed) }) }
	runner := (&fakeSessionRunner{}).handler()
	srv, s := newSessionTestServerConfigured(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if armed.Load() && r.Method == http.MethodPost && r.URL.Path == "/sessions" {
			creates.Add(1)
			entered <- struct{}{}
			<-proceed
		}
		runner.ServeHTTP(w, r)
	}), func(c *config.Config) {
		c.Offers[0].Capacity.MaxInFlight = fixture.MaxInFlight
		c.Offers[0].Capacity.QueueLimit = fixture.QueueLimit
	})
	defer unblock()
	armed.Store(true)
	type opened struct {
		id   string
		resp *http.Response
	}
	results := make(chan opened, fixture.Opens)
	for i := 0; i < fixture.Opens; i++ {
		go func(i int) {
			id := fmt.Sprintf("capacity-%d", i)
			results <- opened{id, sessionOpenWithGatewayID(t, srv, id, "gateway-"+id)}
		}(i)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("opens did not reach runner")
	}
	for i := 0; i < fixture.Opens-fixture.Accepted; i++ {
		select {
		case r := <-results:
			body := decode(t, r.resp)
			if r.resp.StatusCode != fixture.RejectedStatus || r.resp.Header.Get(livepeerheader.Backoff) != fixture.Backoff || r.resp.Header.Get(livepeerheader.Error) != fixture.Error {
				t.Fatalf("capacity rejection=%d %v", r.resp.StatusCode, body)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("over-capacity open waited instead of rejecting")
		}
	}
	// Runner reattachment recovery must not mistake these live opens for crashes.
	s.sessionEngine.Recover(context.Background())
	if got := s.currentBackendInFlight("h1|sfu"); got != fixture.Accepted {
		t.Fatalf("opening occupancy=%d", got)
	}
	unblock()
	for i := 0; i < fixture.Accepted; i++ {
		r := <-results
		body := decode(t, r.resp)
		if r.resp.StatusCode != fixture.OpenStatus {
			t.Fatalf("open=%d %v", r.resp.StatusCode, body)
		}
		replay := sessionOpenWithGatewayID(t, srv, r.id, "gateway-"+r.id)
		replayed := decode(t, replay)
		if replay.StatusCode != fixture.ReplayStatus || replayed["session_id"] != body["session_id"] {
			t.Fatalf("replay=%d %v", replay.StatusCode, replayed)
		}
	}
	if creates.Load() != int32(fixture.Accepted) || s.currentBackendInFlight("h1|sfu") != fixture.Accepted {
		t.Fatalf("creates=%d occupied=%d", creates.Load(), s.currentBackendInFlight("h1|sfu"))
	}
}

func TestRestoreSessionCapacityMigratesOldRecordsAndOpeningIntents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.db")
	key := make([]byte, sessionstore.KeySize)
	st, err := sessionstore.Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []*sessionstore.Record{
		{SessionID: "active", State: sessionstore.StateActive, BackendRef: "cap|offer|host|local"},
		{SessionID: "closing", State: sessionstore.StateWindingDown, BackendRef: "cap|offer|host|local", CapacityRef: "closing-slot"},
		{SessionID: "stopped", State: sessionstore.StateWindingDown, BackendRef: "cap|offer|host|local", RunnerTerminated: true},
		{SessionID: "ended", State: sessionstore.StateEnded, BackendRef: "cap|offer|host|local"},
	} {
		if err = st.Create(r); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.ReserveOpen("opening", []byte("fp")); err != nil {
		t.Fatal(err)
	}
	if err = st.UpdateReservation("opening", func(r *sessionstore.OpenReservation) error {
		r.BackendRef = "cap|offer|host|local"
		r.Stage = sessionstore.ReservationPaid
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = sessionstore.Open(path, key)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := &Server{}
	if err = s.restoreSessionCapacity(st); err != nil {
		t.Fatal(err)
	}
	if err = s.restoreSessionCapacity(st); err != nil {
		t.Fatal(err)
	}
	if n := s.currentBackendInFlight("host|local"); n != 3 {
		t.Fatalf("restored=%d", n)
	}
	cap := &config.Capability{Backend: config.Backend{ID: "host|local", MaxInFlight: 2}}
	if _, ok := s.reserveBackend(cap); ok {
		t.Fatal("lower limit admitted work over restored occupancy")
	}
	r, _ := st.Get("active")
	if r.CapacityRef != "session:active" {
		t.Fatalf("migration=%+v", r)
	}
	s.releaseSessionCapacity(r.CapacityRef)
	s.releaseSessionCapacity(r.CapacityRef)
	if n := s.currentBackendInFlight("host|local"); n != 2 {
		t.Fatalf("duplicate release=%d", n)
	}
	s.releaseSessionCapacity("closing-slot")
	release, ok := s.reserveBackend(cap)
	if !ok {
		t.Fatal("confirmed release did not free slot")
	}
	release()
}

func TestUnroutableRunnerCannotConfirmTermination(t *testing.T) {
	if err := (&unroutableRunner{}).TerminateSession(context.Background(), "runner-id", "close"); err == nil {
		t.Fatal("unreachable runner reported stopped")
	}
}

func TestJobsAndSessionsShareCapacityWithIdempotentRelease(t *testing.T) {
	s := &Server{}
	cap := &config.Capability{Backend: config.Backend{ID: "host|local", MaxInFlight: 1}}
	if err := s.acquireSessionRunner("session-owner", cap); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.reserveBackend(cap); ok {
		t.Fatal("job ignored session occupancy")
	}
	if err := s.acquireSessionRunner("session-owner", cap); err != nil {
		t.Fatal(err)
	}
	if n := s.currentBackendInFlight("host|local"); n != 1 {
		t.Fatalf("duplicate acquisition=%d", n)
	}
	s.releaseSessionCapacity("session-owner")
	release, ok := s.reserveBackend(cap)
	if !ok {
		t.Fatal("job cannot use released slot")
	}
	s.releaseSessionCapacity("session-owner")
	if err := s.acquireSessionRunner("new-session", cap); err == nil {
		t.Fatal("session ignored job occupancy")
	}
	release()
	if err := s.acquireSessionRunner("new-session", cap); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSessionCapacityRejectsUnboundLiveWork(t *testing.T) {
	for _, opening := range []bool{false, true} {
		t.Run(fmt.Sprint(opening), func(t *testing.T) {
			st, err := sessionstore.Open(filepath.Join(t.TempDir(), "sessions.db"), make([]byte, sessionstore.KeySize))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			if opening {
				if err = st.ReserveOpen("unbound", []byte("fp")); err != nil {
					t.Fatal(err)
				}
				if err = st.UpdateReservation("unbound", func(r *sessionstore.OpenReservation) error { r.Stage = sessionstore.ReservationPaid; return nil }); err != nil {
					t.Fatal(err)
				}
			} else {
				if err = st.Create(&sessionstore.Record{SessionID: "unbound", State: sessionstore.StateActive, BackendRef: "cap|offer"}); err != nil {
					t.Fatal(err)
				}
			}
			if err = (&Server{}).restoreSessionCapacity(st); err == nil {
				t.Fatal("unbound work disappeared from restored capacity")
			}
		})
	}
}
