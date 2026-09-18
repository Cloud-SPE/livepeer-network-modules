package registry

import (
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version"
)

// SettlementKeysPayload is GET /registry/settlement-keys: the delegated
// settlement key(s) this broker signs with, each as a self-signed
// announcement (protocols/broker-admin.md §7.1). Public, unauthenticated
// — public keys are public — and read by the coordinator on the same
// scrape as offerings, so an operator never copies a key by hand.
type SettlementKeysPayload struct {
	SpecVersion    string                    `json:"spec_version"`
	OrchEthAddress string                    `json:"orch_eth_address"`
	Keys           []settlement.Announcement `json:"keys"`
}

// BuildSettlementKeys announces the signer's key; a broker with no
// delegated key answers an empty list, which is a fact, not an error.
func BuildSettlementKeys(orchEthAddress, baseURL string, signer *settlement.Signer) SettlementKeysPayload {
	out := SettlementKeysPayload{
		SpecVersion:    version.VERSION,
		OrchEthAddress: orchEthAddress,
		Keys:           []settlement.Announcement{},
	}
	if signer != nil {
		out.Keys = append(out.Keys, signer.Announce(orchEthAddress, baseURL))
	}
	return out
}
