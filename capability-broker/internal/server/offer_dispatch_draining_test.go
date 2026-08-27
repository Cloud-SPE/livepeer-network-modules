package server

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// Draining is a live fact about one host, not a change to what is sold
// (runner-attach §7.1). The broker must stop dispatching to that runner
// while still advertising the offer it serves: a pool withdrawing a
// workload from one host has not changed the offering, so the manifest
// must not flicker and a gateway must get a 503 with a backoff rather
// than a 404 on a tuple that is still on sale elsewhere.
func TestDrainingRunnerLeavesDispatchButKeepsTheOfferAdvertised(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)

	c := dialAttach(t, ts)
	results := runnerSide(t, c, func(string, string, map[string][]string, []byte) (int, string, []byte) {
		return 200, "application/json", []byte(`{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	})
	serving := attachDoc(token, "h1", func(m map[string]any) {
		m["capabilities"].([]any)[0].(map[string]any)["identity"] = map[string]any{"openai.model": "llama"}
	})
	if res := registerVia(t, c, results, serving); res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(offeringsPayloadOf(t, srv)["capabilities"].([]any)) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	advertised, _ := json.Marshal(offeringsPayloadOf(t, srv)["capabilities"])
	if !strings.Contains(string(advertised), "llama-shared") {
		t.Fatalf("offer never froze: %s", advertised)
	}
	if n := len(dispatchBackends(t, srv)); n != 1 {
		t.Fatalf("eligible backends before the drain = %d, want 1", n)
	}

	// The agent sets draining and re-registers, exactly as it does
	// before it stops the container.
	draining := attachDoc(token, "h1", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "llama"}
		cap0["draining"] = true
	})
	if res := registerVia(t, c, results, draining); res["document"] != "accepted" {
		t.Fatalf("the broker refused a document that declares draining: %v\n"+
			"draining is a declared field of a capability entry (runner-attach §7.1), but the "+
			"unknown-field check in internal/runnerattach (capFields) does not list it — so the "+
			"agent's withdrawal re-register throws away the host's WHOLE document, taking every "+
			"other runner on that host down with it instead of quietly draining one", res)
	}
	for time.Now().Before(deadline) {
		if len(dispatchBackends(t, srv)) == 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(dispatchBackends(t, srv)); n != 0 {
		t.Fatalf("draining runner is still in the dispatch group (%d backends); it would be sent new work", n)
	}

	// The manifest is byte-identical: the offer is still sold, and the
	// tuple still carries its price and shape.
	nowAdvertised, _ := json.Marshal(offeringsPayloadOf(t, srv)["capabilities"])
	if string(nowAdvertised) != string(advertised) {
		t.Fatalf("one host draining changed the advertised manifest:\n%s\nvs\n%s", advertised, nowAdvertised)
	}

	// A gateway that buys it gets health, not a missing capability.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/job", strings.NewReader(`{"model":"llama"}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "llama-shared")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "job-draining-1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("paid job to a draining-only offer = %d %s, want 503", resp.StatusCode, body)
	}

	// Clearing the flag is not a shape change either: the same runner
	// comes back to dispatch without re-certifying and without the
	// manifest moving.
	if res := registerVia(t, c, results, serving); res["document"] != "accepted" {
		t.Fatalf("re-attach after the drain: %v", res)
	}
	for time.Now().Before(deadline) {
		if len(dispatchBackends(t, srv)) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(dispatchBackends(t, srv)); n != 1 {
		t.Fatalf("backends after the drain was cleared = %d, want the runner back", n)
	}
	backAdvertised, _ := json.Marshal(offeringsPayloadOf(t, srv)["capabilities"])
	if string(backAdvertised) != string(advertised) {
		t.Fatalf("clearing draining moved the manifest:\n%s\nvs\n%s", advertised, backAdvertised)
	}
}

// dispatchBackends is what the offer would dispatch over right now.
func dispatchBackends(t *testing.T, srv *Server) []*config.Capability {
	t.Helper()
	group, ok := srv.offerGroupFor("openai:chat-completions", "llama-shared")
	if !ok {
		t.Fatal("offer llama-shared is not dispatchable at all; draining must not remove the offer")
	}
	if group.Published == nil {
		t.Fatal("offer has no published tuple")
	}
	return group.Backends
}
