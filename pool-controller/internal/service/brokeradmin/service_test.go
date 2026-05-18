package brokeradmin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

func TestReloadAndConfirm(t *testing.T) {
	reloaded := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/v1/runtime/reload":
			if r.Method != http.MethodPost {
				t.Fatalf("reload method = %s, want POST", r.Method)
			}
			reloaded = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"last_reload_status":"applied"}`))
		case "/admin/v1/runtime":
			if !reloaded {
				t.Fatalf("runtime queried before reload")
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"loaded_revision":"rev-1","last_reload_status":"applied"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, config.AuthConfig{Method: "none"}, time.Second)
	status, err := client.ReloadAndConfirm("rev-1")
	if err != nil {
		t.Fatalf("ReloadAndConfirm() error = %v", err)
	}
	if status == nil || status.LoadedRevision != "rev-1" {
		t.Fatalf("status = %#v", status)
	}
}

func TestReloadAndConfirmRejectsRevisionMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/admin/v1/runtime/reload":
			w.WriteHeader(http.StatusOK)
		case "/admin/v1/runtime":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"loaded_revision":"rev-2","last_reload_status":"applied"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	client := New(srv.URL, config.AuthConfig{Method: "none"}, time.Second)
	if _, err := client.ReloadAndConfirm("rev-1"); err == nil {
		t.Fatal("ReloadAndConfirm() error = nil, want mismatch error")
	}
}
