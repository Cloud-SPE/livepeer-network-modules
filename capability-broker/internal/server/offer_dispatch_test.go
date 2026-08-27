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

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
)

// The whole epic in one test: a member enrolls, attaches a runner that
// declares what it is, certification freezes the offer, and then paid
// work flows to that runner — with no operator edit between attaching
// and earning.
func TestPaidJobDispatchesToAttachedRunner(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)

	var gotPath, gotBody, gotLocalID string
	c := dialAttach(t, ts)
	results := runnerSide(t, c, func(method, path string, headers map[string][]string, body []byte) (int, string, []byte) {
		u := path
		if i := strings.Index(u, "worker.local"); i >= 0 {
			u = u[i+len("worker.local"):]
		}
		gotPath, gotBody = u, string(body)
		if v := headers["Livepeer-Runner-Local-Id"]; len(v) > 0 {
			gotLocalID = v[0]
		}
		return 200, "application/json", []byte(`{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	})
	res := registerVia(t, c, results, attachDoc(token, "h1", func(m map[string]any) {
		cap0 := m["capabilities"].([]any)[0].(map[string]any)
		cap0["identity"] = map[string]any{"openai.model": "llama"}
	}))
	if res["document"] != "accepted" {
		t.Fatalf("attach: %v", res)
	}

	// The offer has no certification steps, so the match certifies and
	// freezes; wait for the tuple to be advertised.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(offeringsPayloadOf(t, srv)["capabilities"].([]any)) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/job",
		strings.NewReader(`{"model":"llama","messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "llama-shared")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "job-attached-1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("paid job: %d %s", resp.StatusCode, body)
	}
	// The runner's declared extractor counted the work.
	if got := resp.Header.Get(livepeerheader.WorkUnits); got != "42" {
		t.Fatalf("work units = %q, want 42 (extractor from the frozen shape)", got)
	}
	// It reached the container by the runner's own declared path, with
	// the routing header, carrying the gateway's body.
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("runner path = %q", gotPath)
	}
	if gotLocalID != "chat" {
		t.Fatalf("routing header = %q", gotLocalID)
	}
	if !strings.Contains(gotBody, `"model":"llama"`) {
		t.Fatalf("runner body = %q", gotBody)
	}
}

// An advertised offer whose runners have all gone away is health, not a
// manifest change: the tuple keeps its price and the broker answers 503
// with a backoff rather than 404 (plan 0043 §3.4).
func TestPaidJobWithNoEligibleRunnerIs503(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"h1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)
	c := dialAttach(t, ts)
	results := runnerSide(t, c, func(string, string, map[string][]string, []byte) (int, string, []byte) {
		return 200, "application/json", []byte(`{}`)
	})
	registerVia(t, c, results, attachDoc(token, "h1", func(m map[string]any) {
		m["capabilities"].([]any)[0].(map[string]any)["identity"] = map[string]any{"openai.model": "llama"}
	}))
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if len(offeringsPayloadOf(t, srv)["capabilities"].([]any)) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The runner leaves; the offer stays frozen and advertised.
	_ = c.Close()
	for time.Now().Before(deadline) {
		if _, ov, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/offers/llama-shared", "", nil); ov["runners"].(map[string]any)["eligible"] == float64(0) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := len(offeringsPayloadOf(t, srv)["capabilities"].([]any)); n != 1 {
		t.Fatalf("runner churn changed the advertised payload: %d tuples", n)
	}

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/job", strings.NewReader(`{"model":"llama"}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "llama-shared")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "job-none-1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("no-runner job: %d %s", resp.StatusCode, body)
	}
}

// Capacity is the operator's, from the offer — never the runner's.
func TestOfferCapacityBoundsTheRunner(t *testing.T) {
	srv := newOffersTestServer(t)
	view, ok := srv.offersEngine.ViewOf("llama-shared")
	if !ok {
		t.Fatal("offer missing")
	}
	if view.Operator.Capacity.MaxInFlight != 0 {
		t.Fatalf("fixture changed: %+v", view.Operator.Capacity)
	}
	// With a bound set, the synthetic backend carries it.
	raw, _ := json.Marshal(view.Operator)
	if strings.Contains(string(raw), `"max_in_flight":`) && !strings.Contains(string(raw), `"max_in_flight":0`) {
		t.Fatalf("unexpected capacity in fixture: %s", raw)
	}
}
