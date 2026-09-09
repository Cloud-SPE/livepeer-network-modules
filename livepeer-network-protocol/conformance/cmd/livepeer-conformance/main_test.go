package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnrollAttachCredentialUsesConfiguredAdminToken(t *testing.T) {
	const wantToken = "pilot-specific-admin-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+wantToken {
			t.Fatalf("Authorization = %q, want configured bearer", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"host_id":"probe-host","credential":{"token":"runner-token"}}`))
	}))
	defer server.Close()

	credential, hostID, err := enrollAttachCredential(server.URL, "probe-host", wantToken)
	if err != nil {
		t.Fatalf("enrollAttachCredential: %v", err)
	}
	if credential != "runner-token" || hostID != "probe-host" {
		t.Fatalf("credential, hostID = %q, %q", credential, hostID)
	}
}

func TestEnrollAttachCredentialRequiresAdminToken(t *testing.T) {
	if _, _, err := enrollAttachCredential("http://unused.invalid", "probe-host", " "); err == nil {
		t.Fatal("enrollAttachCredential accepted an empty admin token")
	}
}
