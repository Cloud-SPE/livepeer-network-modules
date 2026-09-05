package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// A runner that is told `accepted` is attached, at that moment.
//
// The broker used to send the result frame and register the host after,
// so for a window the runner had an acknowledgement the broker had not
// yet honoured: the host was absent from /admin/v1/runners and ConnFor
// found no connection, which is what dispatch uses. Work arriving in
// that window had nowhere to go.
//
// Asserted with no retry loop on purpose. A poll here would pass on the
// broken ordering too, and the point is that no wait is needed.
func TestAttachIsVisibleTheInstantItIsAcknowledged(t *testing.T) {
	srv := newCredentialTestServer(t, true)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	_, enr, _ := adminReq(t, srv, http.MethodPost, "/admin/v1/enroll", `{"host_id":"host-1"}`, nil)
	token := enr["credential"].(map[string]any)["token"].(string)

	c := dialAttach(t, ts)
	defer func() { _ = c.Close() }()
	res := register(t, c, attachDoc(token, "host-1", nil))
	if res["document"] != "accepted" {
		t.Fatalf("setup: %v", res)
	}

	code, list, _ := adminReq(t, srv, http.MethodGet, "/admin/v1/runners", "", nil)
	runners := list["runners"].([]any)
	if code != http.StatusOK || len(runners) != 1 {
		t.Fatalf("the broker acknowledged an attach it had not yet recorded: %d %v", code, list)
	}

	// And the dispatch path, which is what the acknowledgement actually
	// promises: the capability is reachable.
	if _, ok := srv.runners.ConnFor("host-1", "chat"); !ok {
		t.Fatal("the acknowledged runner is not dispatchable; work sent now would find no connection")
	}
}
