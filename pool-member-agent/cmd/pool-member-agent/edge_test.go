package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
)

// The edge routes /r/<local_id>/<rest> to that runner's own address with
// the prefix stripped, refuses an id the host does not run, and follows
// the live runner set rather than a snapshot of it.
func TestEdgeRoutesByLocalID(t *testing.T) {
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "path="+r.URL.Path+" q="+r.URL.RawQuery)
	}))
	defer runner.Close()
	state := newRunnerState()
	state.set([]attach.Runner{{LocalID: "nemo", URL: runner.URL}}, "r1")
	edge := httptest.NewServer(edgeHandler(state))
	defer edge.Close()

	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(edge.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/r/nemo/v1/sessions/s1/stream?x=1"); code != 200 || body != "path=/v1/sessions/s1/stream q=x=1" {
		t.Fatalf("routed: %d %q", code, body)
	}
	if code, _ := get("/r/ghost/v1/x"); code != http.StatusNotFound {
		t.Fatalf("unknown local id = %d, want 404", code)
	}
	if code, _ := get("/healthz"); code != 200 {
		t.Fatalf("healthz = %d", code)
	}
	// The pool withdraws the runner: the route goes with it.
	state.set(nil, "r2")
	if code, _ := get("/r/nemo/v1/x"); code != http.StatusNotFound {
		t.Fatalf("withdrawn runner still routed: %d", code)
	}
}
