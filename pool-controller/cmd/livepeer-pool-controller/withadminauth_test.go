package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	adminserver "github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/server/admin"
)

// TestWithAdminAuthSessionOrBearer verifies the /admin/v1 auth chokepoint
// accepts either the admin bearer token (scripts) or a live login session
// cookie (browser operators).
func TestWithAdminAuthSessionOrBearer(t *testing.T) {
	state := &runtimeState{adminToken: "secret"}
	state.session = adminserver.NewSessionAuth(func() string {
		state.mu.RLock()
		defer state.mu.RUnlock()
		return state.adminToken
	})
	sid, err := state.session.Login("secret", "mike")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}

	handler := withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	cases := []struct {
		name string
		set  func(*http.Request)
		want int
	}{
		{"no auth", func(*http.Request) {}, http.StatusUnauthorized},
		{"bearer ok", func(r *http.Request) { r.Header.Set("Authorization", "Bearer secret") }, http.StatusOK},
		{"bearer wrong", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, http.StatusUnauthorized},
		{"session cookie ok", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: adminserver.SessionCookieName, Value: sid})
		}, http.StatusOK},
		{"session cookie bad", func(r *http.Request) {
			r.AddCookie(&http.Cookie{Name: adminserver.SessionCookieName, Value: "deadbeef"})
		}, http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/admin/v1/offers", nil)
			c.set(req)
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != c.want {
				t.Fatalf("status = %d; want %d", rec.Code, c.want)
			}
		})
	}
}

// TestWithAdminAuthOpenMode verifies that an empty admin token leaves the API
// open (matching prior behavior).
func TestWithAdminAuthOpenMode(t *testing.T) {
	state := &runtimeState{adminToken: ""}
	handler := withAdminAuth(state, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/admin/v1/offers", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("open-mode status = %d; want 200", rec.Code)
	}
}
