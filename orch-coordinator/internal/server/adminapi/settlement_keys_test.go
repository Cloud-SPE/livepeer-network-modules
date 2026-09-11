package adminapi

import (
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/settlementkeys"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

func TestSettlementKeyRows_StatesAndAlerts(t *testing.T) {
	now := time.Now().UTC()
	brokers := []scrape.BrokerStatus{
		{Name: "a", BaseURL: "https://a", SettlementKeysSupported: true, SettlementKeys: []settlementkeys.Discovered{{PublicKey: "0x04aa", Proven: true}}},
		{Name: "b", BaseURL: "https://b", SettlementKeysSupported: true, SettlementKeys: []settlementkeys.Discovered{{PublicKey: "0x04bb", Proven: true}}},
		{Name: "c", BaseURL: "https://c", SettlementKeysSupported: true, SettlementKeys: []settlementkeys.Discovered{{PublicKey: "0x04cc", Proven: false, Reason: "bad proof"}}},
		{Name: "d", BaseURL: "https://d", SettlementKeysSupported: true},
		{Name: "e", BaseURL: "https://e", SettlementKeysSupported: false},
	}
	cand := &types.ManifestPayload{SettlementKeys: []types.SettlementKey{
		{PublicKey: "0x04aa", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
		{PublicKey: "0x04bb", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
		{PublicKey: "0x04ff", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
	}}
	pub := &types.ManifestPayload{SettlementKeys: []types.SettlementKey{
		{PublicKey: "0x04aa", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
		{PublicKey: "0x04ff", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
		{PublicKey: "0x04ee", NotBefore: now, ExpiresAt: now.Add(time.Hour)},
	}}
	rows := settlementKeyRows(brokers, cand, pub)
	states := map[string]string{}
	for _, r := range rows {
		states[r.Broker+"|"+r.PublicKey] = r.State
	}
	want := map[string]string{
		"a|0x04aa": "published", "b|0x04bb": "pending", "c|0x04cc": "unproven",
		"d|": "none", "e|": "unsupported", "|0x04ff": "published", "|0x04ee": "retiring",
	}
	for k, v := range want {
		if states[k] != v {
			t.Fatalf("%s: state %q, want %q (all: %v)", k, states[k], v, states)
		}
	}
	alerts := collectSettlementKeyAlerts(rows)
	joined := ""
	for _, a := range alerts {
		joined += a.Message + "\n"
	}
	for _, want := range []string{"b settlement key", "not yet published", "c announced a settlement key it could not prove", "d signs no settlements", "e cannot announce"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("alerts lack %q:\n%s", want, joined)
		}
	}
	for _, a := range alerts {
		if strings.HasPrefix(a.Message, "a ") {
			t.Fatalf("published key alerted: %s", a.Message)
		}
	}
}
