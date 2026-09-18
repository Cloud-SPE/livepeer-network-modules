package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func TestDeviceDrainScopeAndStaleDispatchPreserveSibling(t *testing.T) {
	dir := t.TempDir()
	ledger, err := workledger.Open(filepath.Join(dir, "work.db"), "pool", "source", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	creds, err := credentialstore.Open(filepath.Join(dir, "credentials.db"), make([]byte, 32), credentialstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer creds.Close()
	entry := credentialstore.SyncEntry{CredentialID: "credential", HostID: "host", PoolID: "pool", TokenSHA256: credentialstore.HashToken("secret"), ExpiresAt: time.Now().Add(time.Hour), DeviceOwnership: map[string]uint64{"gpu-a": 1, "gpu-b": 2}}
	if _, err := creds.SyncReplace("one", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	token, digest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth.json")
	credential := serviceauth.Credential{ID: "controller", PoolID: "pool", Roles: []string{"controller"}, Resources: []string{"broker"}, TokenSHA256: digest, ExpiresAt: time.Now().Add(time.Hour)}
	save := func() {
		raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{credential}})
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save()
	srv := &Server{cfg: &config.Config{PoolID: "pool", ServiceResource: "broker", ServiceAuthFile: path, Ownership: api.Config{URL: "https://ownership.example"}}, credentialStore: creds, ownershipChecks: map[ownershipCacheKey]uint64{{"host", "gpu-a"}: 1, {"host", "gpu-b"}: 2}, workAccounting: &workledger.Client{Store: ledger}}
	action := "drain"
	call := func(pool string, generation uint64) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{"action": action, "pool_id": pool, "enrollment_id": "host", "device_id": "GPU-A", "generation": generation, "reason": "regional transfer"})
		r := httptest.NewRequest("POST", "/admin/v1/devices/drain", strings.NewReader(string(raw)))
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set(serviceauth.PoolHeader, pool)
		w := httptest.NewRecorder()
		srv.handleDeviceDrain(w, r)
		return w
	}
	if w := call("other", 1); w.Code != 401 {
		t.Fatal("wrong pool mutated drain", w.Code)
	}
	credential.Roles = []string{"revenue-reader"}
	save()
	if w := call("pool", 1); w.Code != 401 {
		t.Fatal("reader mutated drain", w.Code)
	}
	credential.Roles = []string{"controller"}
	save()
	if w := call("pool", 3); w.Code != 409 {
		t.Fatal("wrong generation mutated drain", w.Code)
	}
	if w := call("pool", 1); w.Code != 200 || !strings.Contains(w.Body.String(), `"source_id":"source"`) {
		t.Fatalf("qualified drain: %d %s", w.Code, w.Body.String())
	}
	if srv.authorizeDeviceDispatch("host", runnerattach.Capability{Devices: []string{"gpu-a"}}) || !srv.authorizeDeviceDispatch("host", runnerattach.Capability{Devices: []string{"gpu-b"}}) {
		t.Fatal("drain admitted stale selection or blocked sibling")
	}
	delete(entry.DeviceOwnership, "gpu-a")
	if _, err := creds.SyncReplace("two", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	action = "revoke"
	if w := call("pool", 1); w.Code != 200 || !strings.Contains(w.Body.String(), `"revoked":true`) {
		t.Fatalf("revoke proof %d %s", w.Code, w.Body.String())
	}
	action = "drain"
	if w := call("pool", 1); w.Code != 200 {
		t.Fatal("revocation made durable drain proof inaccessible", w.Code)
	}
	credential.Revoked = true
	save()
	if w := call("pool", 1); w.Code != 401 {
		t.Fatal("revoked service credential accepted", w.Code)
	}
}
