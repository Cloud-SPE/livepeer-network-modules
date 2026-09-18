package ownership

import (
	"encoding/json"
	"encoding/pem"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestSelectedDestinationClaimDoesNotTouchSourceSibling(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	owner, err := repo.OpenOwnership(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if _, err := owner.Change("claim", "source", api.Request{DeviceID: "gpu-a", PoolID: "source", EnrollmentID: "source-host", MemberWallet: "member"}); err != nil {
		t.Fatal(err)
	}
	if err := st.PutHostEnrollment(types.HostEnrollment{ID: "target-host", MemberEthAddress: "member", Status: types.HostEnrollmentActive, GPUUUIDs: []string{"gpu-b"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	token, digest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	authPath, tokenPath, caPath := filepath.Join(dir, "auth"), filepath.Join(dir, "token"), filepath.Join(dir, "ca")
	raw, _ := json.Marshal(serviceauth.File{Credentials: []serviceauth.Credential{{ID: "destination", PoolID: st.PoolID(), TokenSHA256: digest, Roles: []string{"ownership-controller"}, Resources: []string{"ownership"}, ExpiresAt: time.Now().Add(time.Hour)}}})
	if err := os.WriteFile(authPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(Handler(owner, serviceauth.Verifier{Path: authPath}))
	defer server.Close()
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	guard, err := NewGuard(st, api.Config{URL: server.URL, TokenFile: tokenPath, CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	unit := types.HardwareUnit{ID: "target-a", GPUUUID: "gpu-a", EnrollmentID: "target-host", MemberEthAddress: "member"}
	if err := guard.PutHardwareUnit(unit); err == nil {
		t.Fatal("unselected source GPU claimed")
	}
	unit.ID = "target-b"
	unit.GPUUUID = "GPU-B"
	if err := guard.PutHardwareUnit(unit); err != nil {
		t.Fatal(err)
	}
	source, _ := owner.Get("gpu-a")
	target, _ := owner.Get("gpu-b")
	if source.PoolID != "source" || source.Generation != 1 || target.PoolID != st.PoolID() || target.Generation != 1 {
		t.Fatalf("ownership crossed %+v %+v", source, target)
	}
}
