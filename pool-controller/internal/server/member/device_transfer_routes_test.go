package member

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func TestMemberTransferCannotCrossWalletPoolOrOrigin(t *testing.T) {
	f := newDesiredStateFixture(t)
	unit, err := f.repo.GetHardwareUnit("unit-a")
	if err != nil {
		t.Fatal(err)
	}
	unit.OwnershipGeneration = 1
	if err := f.repo.SaveHardwareOwnership(unit); err != nil {
		t.Fatal(err)
	}
	source := revenue.Source{PoolID: f.repo.PoolID(), SourceID: "0x" + strings.Repeat("a", 64), BrokerID: "broker", ChainID: 42161, Payee: f.host.MemberEthAddress, URL: "https://broker.example"}
	sessions := NewSessionAuth()
	own, err := sessions.Create(f.host.MemberEthAddress)
	if err != nil {
		t.Fatal(err)
	}
	other, err := sessions.Create(f.other.MemberEthAddress)
	if err != nil {
		t.Fatal(err)
	}
	deps := Deps{Repo: f.repo, Sessions: sessions, BeginTransfer: func(_ context.Context, unit, wallet, destination, reason string) (repo.DeviceTransfer, error) {
		return f.repo.BeginDeviceTransfer(unit, wallet, destination, reason, []revenue.Source{source})
	}}
	mux := http.NewServeMux()
	registerDeviceTransferRoutes(mux, deps)
	for _, tc := range []struct {
		token, origin, pool string
		code                int
	}{{"", "https://members.example", f.repo.PoolID(), 401}, {own, "https://attacker.example", f.repo.PoolID(), 403}, {own, "https://members.example", "another-pool", 400}, {other, "https://members.example", f.repo.PoolID(), 409}, {own, "https://members.example", f.repo.PoolID(), 202}} {
		req := httptest.NewRequest("POST", "https://members.example/member/v1/hardware/unit-a/transfer", strings.NewReader(fmt.Sprintf(`{"pool_id":%q,"destination_pool_id":"destination","reason":"move GPU"}`, tc.pool)))
		req.Header.Set("Origin", tc.origin)
		req.AddCookie(&http.Cookie{Name: memberSessionCookieName, Value: tc.token})
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		if w.Code != tc.code {
			t.Fatalf("status %d want %d: %s", w.Code, tc.code, w.Body.String())
		}
	}
	req := httptest.NewRequest("GET", "https://members.example/member/v1/device-transfers", nil)
	req.AddCookie(&http.Cookie{Name: memberSessionCookieName, Value: other})
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != 200 || strings.Contains(w.Body.String(), unit.GPUUUID) {
		t.Fatal("cross-member transfer history exposed", w.Body.String())
	}
}

func TestMemberTransferProjectionHidesOperatorProofs(t *testing.T) {
	item := repo.DeviceTransfer{ID: "transfer", Phase: "draining", LastError: "cannot read /private/operator-token", Targets: []repo.DeviceTransferTarget{{Source: revenue.Source{URL: "https://private-admin.example", TokenFile: "/private/operator-token"}}}}
	raw, err := json.Marshal(transferView(item))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "targets") || !strings.Contains(string(raw), "Waiting for work") {
		t.Fatal(string(raw))
	}
}
