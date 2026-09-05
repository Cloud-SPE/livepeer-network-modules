package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/poolsnapshot"
)

// Selection over an offer's eligible runners.
//
// Certification already decided WHETHER a runner may serve the offer, so
// nothing here re-litigates that. What is left is the choice BETWEEN
// eligible runners: the operator's capacity bound and the pool
// controller's snapshot, which is the seam the fairness ladder feeds.

const selectionOfferingID = "sel-shared"

// newRunnerSelectionServer brings up an offers broker whose one offer
// bounds each eligible runner at maxInFlight (0 = unbounded), then
// attaches two runners that both certify onto the frozen shape.
func newRunnerSelectionServer(t *testing.T, maxInFlight int) *Server {
	t.Helper()
	t.Setenv("BROKER_ADMIN_TOKEN", "secret-token")
	dir := t.TempDir()
	// The offers state file is written as a hijacked attach connection
	// unwinds, which httptest.Server.Close does not wait for; keep it
	// out of t.TempDir so the write cannot race RemoveAll.
	stateDir, err := os.MkdirTemp("", "selection-state-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 50; i++ {
			if os.RemoveAll(stateDir) == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, []byte(strings.Repeat("ab", 32)), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "host-config.yaml")
	cfg := `
identity:
  orch_eth_address: 0x1234567890abcdef1234567890abcdef12345678
external_base_url: https://broker.example
admin_auth:
  method: bearer
  secret_ref: env://BROKER_ADMIN_TOKEN
listen:
  paid: ":8080"
  metrics: ":9090"
payment_daemon:
  mock: true
credential_store:
  path: ` + filepath.Join(dir, "creds.db") + `
  sealing_key_file: ` + keyPath + `
offers_state_path: ` + filepath.Join(stateDir, "offers-state.json") + `
offers:
  - offering_id: ` + selectionOfferingID + `
    capability: openai:chat-completions
    protocol: paid-job/v1
    match: { identity.openai.model: llama }
    price: { amount_wei: "1", per_units: 1 }
    capacity: { max_in_flight: ` + fmt.Sprint(maxInFlight) + ` }
    extra: { provider: vllm, openai: { model: llama } }
`
	if err := os.WriteFile(configPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := mustServerFromPath(t, configPath)
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)

	for _, hostID := range []string{"h1", "h2"} {
		_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll",
			`{"host_id":"`+hostID+`"}`, nil)
		token := enr["credential"].(map[string]any)["token"].(string)
		c := dialAttach(t, ts)
		res := register(t, c, attachDoc(token, hostID, func(m map[string]any) {
			m["capabilities"].([]any)[0].(map[string]any)["identity"] =
				map[string]any{"openai.model": "llama"}
		}))
		if res["document"] != "accepted" {
			t.Fatalf("attach %s: %v", hostID, res)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(srv.offersEngine.EligiblePairs(selectionOfferingID)) == 2 {
			return srv
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("runners never became eligible: %v", srv.offersEngine.EligiblePairs(selectionOfferingID))
	return nil
}

func selectionGroup(t *testing.T, s *Server) *capabilityGroup {
	t.Helper()
	group, ok := s.groupFor("openai:chat-completions", selectionOfferingID)
	if !ok {
		t.Fatal("groupFor() = not found, want found")
	}
	if len(group.Backends) != 2 {
		t.Fatalf("group has %d backends, want both runners", len(group.Backends))
	}
	return group
}

func TestSelectBackendUsesWeightedPickFunction(t *testing.T) {
	s := newRunnerSelectionServer(t, 0)
	// Every eligible runner carries the same weight with no snapshot, so
	// the last slot of the cumulative range belongs to the last runner.
	s.randIntn = func(n int) int { return n - 1 }

	selected, err := s.selectBackend(selectionGroup(t, s))
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "h2|chat" {
		t.Fatalf("selected backend = %q, want h2|chat", got)
	}
}

func TestSelectBackendSkipsBackendAtMaxInFlight(t *testing.T) {
	s := newRunnerSelectionServer(t, 1)
	// The offer's capacity is what bounds a runner — the runner never
	// declares its own (plan 0043 §8).
	s.backendInFlight = map[string]int{"h1|chat": 1}

	selected, err := s.selectBackend(selectionGroup(t, s))
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "h2|chat" {
		t.Fatalf("selected backend = %q, want h2|chat", got)
	}
}

func TestReserveBackendHonorsMaxInFlightAndReleases(t *testing.T) {
	cap := &config.Capability{
		Backend: config.Backend{ID: "backend-a", MaxInFlight: 1},
	}
	s := &Server{backendInFlight: map[string]int{}}

	release, ok := s.reserveBackend(cap)
	if !ok {
		t.Fatal("first reserveBackend() = false, want true")
	}
	if _, ok := s.reserveBackend(cap); ok {
		t.Fatal("second reserveBackend() = true, want false while backend is full")
	}
	release()
	if _, ok := s.reserveBackend(cap); !ok {
		t.Fatal("reserveBackend() after release = false, want true")
	}
}

func TestSelectBackendRespectsPoolExcludedState(t *testing.T) {
	s := newRunnerSelectionServer(t, 0)
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"h1|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"excluded","effective_selection_score":0.5},
			{"backend_id":"h2|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"eligible","effective_selection_score":0.5}
		]
	}`, time.Now().UTC().Format(time.RFC3339), selectionOfferingID, selectionOfferingID))
	if status := s.poolSnapshot.StatusFor("h2|chat", "openai:chat-completions", selectionOfferingID); !status.EntryFound || status.SnapshotStatus != "fresh" || status.EntryEffectiveSelectionScore != 0.5 {
		t.Fatalf("unexpected h2|chat pool status: %+v", status)
	}

	selected, err := s.selectBackend(selectionGroup(t, s))
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "h2|chat" {
		t.Fatalf("selected backend = %q, want h2|chat", got)
	}
}

func TestSelectBackendAppliesPoolScoreWeight(t *testing.T) {
	s := newRunnerSelectionServer(t, 0)
	s.randIntn = func(n int) int { return 60 }
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"h1|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"eligible","effective_selection_score":0.1},
			{"backend_id":"h2|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"eligible","effective_selection_score":0.9}
		]
	}`, time.Now().UTC().Format(time.RFC3339), selectionOfferingID, selectionOfferingID))
	if status := s.poolSnapshot.StatusFor("h2|chat", "openai:chat-completions", selectionOfferingID); !status.EntryFound {
		t.Fatalf("h2|chat pool status not found: %+v", status)
	}

	selected, err := s.selectBackend(selectionGroup(t, s))
	if err != nil {
		t.Fatalf("selectBackend() error = %v", err)
	}
	if got := selected.Backend.ID; got != "h2|chat" {
		t.Fatalf("selected backend = %q, want h2|chat", got)
	}
}

func TestSelectBackendRecordsExhaustedReasonWhenAllCandidatesBlocked(t *testing.T) {
	s := newRunnerSelectionServer(t, 0)
	s.poolSnapshot = loadTestPoolSnapshot(t, fmt.Sprintf(`{
		"generated_at":%q,
		"entries":[
			{"backend_id":"h1|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"excluded","routing_reason":"pool_cooldown","exclusion_reason":"pool_cooldown","effective_selection_score":0.5},
			{"backend_id":"h2|chat","capability_id":"openai:chat-completions","offering_id":%q,"state":"excluded","routing_reason":"pool_cooldown","exclusion_reason":"pool_cooldown","effective_selection_score":0.5}
		]
	}`, time.Now().UTC().Format(time.RFC3339), selectionOfferingID, selectionOfferingID))

	selected, err := s.selectBackend(selectionGroup(t, s))
	if err == nil {
		t.Fatalf("selectBackend() error = nil, selected = %+v; want exhaustion error", selected)
	}
}

func loadTestPoolSnapshot(t *testing.T, body string) *poolsnapshot.Cache {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(ts.Close)

	cache, err := poolsnapshot.New(config.PoolSnapshot{
		URL:            ts.URL,
		TimeoutMS:      100,
		PollIntervalMS: 1000,
		StaleAfterMS:   15000,
		ExpireAfterMS:  60000,
	}, nil)
	if err != nil {
		t.Fatalf("poolsnapshot.New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		cache.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if status := cache.StatusFor("h1|chat", "openai:chat-completions", selectionOfferingID); status.EntryFound || status.SnapshotStatus == "fresh" {
			return cache
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for pool snapshot cache to populate")
	return nil
}
