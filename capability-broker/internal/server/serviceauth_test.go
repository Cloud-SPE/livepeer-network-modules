package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func TestBrokerRegionalRoles(t *testing.T) {
	token, hash, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "services.json")
	cfg := &config.Config{PoolID: "pool_us", ServiceResource: "audio-broker", ServiceAuthFile: path}
	srv := &Server{cfg: cfg}
	for _, tc := range []struct {
		role, pool, method, path string
		allowed                  bool
	}{
		{"controller", "pool_us", "PUT", "/admin/v1/credentials", true},
		{"controller", "pool_us", "PUT", "/admin/v1/offers", true},
		{"coordinator", "pool_us", "GET", "/admin/v1/offers", true},
		{"coordinator", "pool_us", "PUT", "/admin/v1/offers", false},
		{"revenue-reader", "pool_us", "GET", "/admin/v1/credentials", false},
		{"revenue-reader", "pool_us", "POST", "/admin/v1/enroll", false},
		{"controller", "pool_eu", "PUT", "/admin/v1/offers", false},
	} {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{{ID: "caller", TokenSHA256: hash, PoolID: tc.pool, Roles: []string{tc.role}, Resources: []string{"audio-broker"}, ExpiresAt: time.Now().Add(time.Hour)}}})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(tc.method, tc.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set(serviceauth.PoolHeader, "pool_us")
		if got := srv.requireAdminAuth(httptest.NewRecorder(), req); got != tc.allowed {
			t.Fatalf("%+v allowed=%v", tc, got)
		}
	}
}
