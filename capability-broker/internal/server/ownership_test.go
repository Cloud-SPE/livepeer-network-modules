package server

import (
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/runnerattach"
	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"path/filepath"
	"testing"
	"time"
)

func TestDeviceRevocationPreservesSiblingAndRestartFences(t *testing.T) {
	st, err := credentialstore.Open(filepath.Join(t.TempDir(), "credentials.db"), make([]byte, 32), credentialstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	entry := credentialstore.SyncEntry{CredentialID: "credential", HostID: "host", PoolID: "pool_eu", TokenSHA256: credentialstore.HashToken("secret"), ExpiresAt: time.Now().Add(time.Hour), DeviceOwnership: map[string]uint64{"gpu-a": 1, "gpu-b": 2}}
	if _, err := st.SyncReplace("one", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	srv := &Server{cfg: &config.Config{PoolID: "pool_eu", Ownership: api.Config{URL: "https://unavailable.invalid"}}, credentialStore: st, ownershipChecks: map[ownershipCacheKey]uint64{{"host", "gpu-a"}: 1, {"host", "gpu-b"}: 2}}
	a := runnerattach.Capability{Devices: []string{"GPU-a"}}
	b := runnerattach.Capability{Devices: []string{"GPU-b"}}
	if !srv.authorizeDeviceDispatch("host", a) || !srv.authorizeDeviceDispatch("host", b) {
		t.Fatal("validated running grants lost during authority outage")
	}
	delete(entry.DeviceOwnership, "gpu-a")
	if _, err := st.SyncReplace("two", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	srv.pruneOwnershipChecks()
	if srv.authorizeDeviceDispatch("host", a) || !srv.authorizeDeviceDispatch("host", b) {
		t.Fatal("device revocation affected sibling or permitted old device")
	}
	entry.DeviceOwnership["gpu-a"] = 1
	if _, err := st.SyncReplace("stale", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	if srv.authorizeDeviceDispatch("host", a) {
		t.Fatal("replayed grant bypassed authority validation")
	}
	srv.ownershipChecks = nil
	if srv.authorizeDeviceDispatch("host", b) {
		t.Fatal("restart trusted persisted grant without authority validation")
	}
}
