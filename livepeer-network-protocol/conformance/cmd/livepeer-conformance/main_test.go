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

func TestSelectOfferingsLimitsLongLivedRunner(t *testing.T) {
	all := []offering{
		{capabilityID: "conformance:job", offeringID: "all"},
		{capabilityID: "conformance:job", offeringID: "slow"},
		{capabilityID: "conformance:session", offeringID: "default"},
	}
	got, err := selectOfferings(all, []string{"conformance:job/all", "conformance:session/default"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].offeringID != "all" || got[1].offeringID != "default" {
		t.Fatalf("selected offerings = %+v", got)
	}
	if _, err := selectOfferings(all, []string{"conformance:job/missing"}); err == nil {
		t.Fatal("unknown selector passed validation")
	}
}
