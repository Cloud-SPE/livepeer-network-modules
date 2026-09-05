package desiredstate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// A poll that changes nothing must cost one conditional request and no
// body: this loop runs on every enrolled host, forever.
func TestFetchSendsIfNoneMatchAfterTheFirstSuccessAndReportsUnchanged(t *testing.T) {
	var gotAuth []string
	var gotIfNoneMatch []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/member/v1/enrollments/host-1/desired-state" {
			t.Errorf("desired-state path = %q", r.URL.Path)
		}
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		gotIfNoneMatch = append(gotIfNoneMatch, r.Header.Get("If-None-Match"))
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"rev-1"`)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Document{
			EnrollmentID: "host-1",
			Revision:     "rev-1",
			Services:     []Service{{Name: "runner-a", ComposeFragment: "  runner-a:\n", AssignmentID: "unit-a|chat-a"}},
		})
	}))
	defer server.Close()

	client := New(server.URL, "host-1", "token-abc", time.Second)
	doc, err := client.Fetch(context.Background())
	if err != nil {
		t.Fatalf("first Fetch() error = %v", err)
	}
	if doc.Revision != "rev-1" || len(doc.Services) != 1 {
		t.Fatalf("first Fetch() = %+v", doc)
	}

	if _, err := client.Fetch(context.Background()); !errors.Is(err, ErrUnchanged) {
		t.Fatalf("second Fetch() error = %v, want ErrUnchanged", err)
	}

	if len(gotIfNoneMatch) != 2 {
		t.Fatalf("requests = %d, want 2", len(gotIfNoneMatch))
	}
	if gotIfNoneMatch[0] != "" {
		t.Fatalf("first request sent If-None-Match %q with nothing to condition on", gotIfNoneMatch[0])
	}
	if gotIfNoneMatch[1] != `"rev-1"` {
		t.Fatalf("second request If-None-Match = %q, want the revision it last saw", gotIfNoneMatch[1])
	}
	for i, auth := range gotAuth {
		if auth != "Bearer token-abc" {
			t.Fatalf("request %d Authorization = %q, want the enrollment token", i, auth)
		}
	}
}

// A rejected poll must not be mistaken for "nothing changed", or a host
// whose enrollment was revoked would sit on its last document forever
// believing it was current.
func TestFetchReportsAnErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "valid enrollment bearer token is required", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := New(server.URL, "host-1", "stale", time.Second)
	_, err := client.Fetch(context.Background())
	if err == nil || errors.Is(err, ErrUnchanged) {
		t.Fatalf("Fetch() error = %v, want a real failure", err)
	}
}

// The report is the controller's only evidence of what the host
// achieved, so it travels as JSON under the same credential.
func TestReportPostsTheStatusUnderTheEnrollmentToken(t *testing.T) {
	var gotBody []byte
	var gotAuth, gotPath, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAuth = r.Method, r.URL.Path, r.Header.Get("Authorization")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := New(server.URL, "host-1", "token-abc", time.Second)
	err := client.Report(context.Background(), StatusReport{
		Revision: "rev-1",
		Services: []ServiceStatus{{Name: "runner-a", Status: StatusRunning}},
	})
	if err != nil {
		t.Fatalf("Report() error = %v", err)
	}
	if gotMethod != http.MethodPost || gotPath != "/member/v1/enrollments/host-1/status" {
		t.Fatalf("report went to %s %s", gotMethod, gotPath)
	}
	if gotAuth != "Bearer token-abc" {
		t.Fatalf("report Authorization = %q", gotAuth)
	}
	var sent StatusReport
	if err := json.Unmarshal(gotBody, &sent); err != nil {
		t.Fatalf("report body = %q: %v", gotBody, err)
	}
	if sent.Revision != "rev-1" || len(sent.Services) != 1 || sent.Services[0].Status != StatusRunning {
		t.Fatalf("report body = %+v", sent)
	}
}

// A rejected report must surface: the controller not recording what
// happened is exactly the case the loop needs to retry.
func TestReportSurfacesARejection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer server.Close()

	client := New(server.URL, "host-1", "token", time.Second)
	if err := client.Report(context.Background(), StatusReport{Revision: "rev-1"}); err == nil {
		t.Fatal("Report() swallowed a 400")
	}
}
