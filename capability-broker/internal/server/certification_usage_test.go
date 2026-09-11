package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/certification"
)

// postTapEvent is a runner reporting usage to a certification callback.
func postTapEvent(t *testing.T, base, tapID, token, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+certification.TapPathPrefix+tapID,
		strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST tap: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// The callback surface must not become a way to learn which runs are
// open. An unknown tap, a bad token, and a missing token are one answer.
func TestCertificationUsageCallbackIsUniformlyUnauthorized(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	const envelope = `{"event_id":"e1","sequence":1,"event_type":"usage","usage":{"unit":"seconds","total":5}}`
	cases := []struct {
		name, tapID, token string
	}{
		{"unknown tap", "certtap_deadbeef", "certcb_whatever"},
		{"no token", "certtap_deadbeef", ""},
		{"malformed tap id", "not-a-tap-id", "certcb_whatever"},
	}
	// A request with no id at all is not in this table: it does not
	// name a tap, so the mux never routes it and 404 is the honest
	// answer. What must not be distinguishable is one tap from another.
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := postTapEvent(t, ts.URL, tc.tapID, tc.token, envelope); got != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401 — this surface must not distinguish its refusals", got)
			}
		})
	}
}

// A malformed body is a client error, not a silent accept: a runner
// sending the wrong shape should learn that it did.
func TestCertificationUsageCallbackRejectsAMalformedBody(t *testing.T) {
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	if got := postTapEvent(t, ts.URL, "certtap_x", "certcb_x", "not json"); got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", got)
	}
}

// The route exists and carries the path the engine hands to runners. If
// these two ever drift, every session certification silently fails with
// the runner reporting into a 404.
func TestCertificationTapURLMatchesTheRegisteredRoute(t *testing.T) {
	url := certification.TapURL("https://broker.example/", "certtap_abc")
	want := "https://broker.example" + certification.TapPathPrefix + "certtap_abc"
	if url != want {
		t.Fatalf("TapURL() = %q, want %q", url, want)
	}
	srv := newOffersTestServer(t)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()
	// A 401 proves the route is registered and reached the handler; a
	// 404 would mean the mux never heard of it.
	if got := postTapEvent(t, ts.URL, "certtap_abc", "certcb_abc",
		`{"event_id":"e","sequence":1}`); got == http.StatusNotFound {
		t.Fatal("the callback path the engine hands runners is not a registered route")
	}
}

// A broker with no external_base_url mints no callback, so the URL it
// would hand a runner is empty rather than a bare path the runner would
// resolve against itself.
func TestTapURLIsEmptyWithoutABase(t *testing.T) {
	if got := certification.TapURL("", "certtap_abc"); got != "" {
		t.Fatalf("TapURL() = %q, want empty", got)
	}
}
