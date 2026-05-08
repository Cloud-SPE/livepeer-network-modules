// Package chaincommonsadapter implements service-registry-daemon's
// signer.Signer interface by delegating to chain-commons' Keystore.
//
// The adapter is plan 0005's seam: when 0005 lands, cmd/main.go swaps
// the constructor from signer.NewKeystore (V3 JSON unlock + sign) to
// chaincommonsadapter.New (which wraps a chain-commons keystore.Keystore
// and exposes the same Signer surface). All downstream service/ code is
// unchanged because the interface stays the same.
//
// Pre-drafted ahead of plan 0005 so the adapter is reviewable in
// isolation, builds against chain-commons v0.2.0, and proves the
// interface-mapping is sound. Not yet wired into the daemon's main —
// that's plan 0005's responsibility.
package chaincommonsadapter

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/providers/keystore"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// New wraps a chain-commons keystore.Keystore as a registry-daemon
// signer.Signer. Returns an error if ks is nil.
//
// The wrapped Keystore's Sign method must produce EIP-191 personal-sign
// signatures (the chain-commons contract). All v3json and FakeKeystore
// implementations satisfy that. HSM impls that don't expose EIP-191
// would not be compatible — that's a configuration error the caller
// must surface separately.
func New(ks keystore.Keystore) (signer.Signer, error) {
	if ks == nil {
		return nil, errors.New("chaincommonsadapter.New: ks is required")
	}
	addrHex := strings.ToLower(ks.Address().Hex())
	addr, err := types.ParseEthAddress(addrHex)
	if err != nil {
		return nil, fmt.Errorf("chaincommonsadapter.New: address %q from keystore did not parse: %w", addrHex, err)
	}
	return &adapter{ks: ks, address: addr}, nil
}

type adapter struct {
	ks      keystore.Keystore
	address types.EthAddress
}

// Address implements signer.Signer.
func (a *adapter) Address() types.EthAddress { return a.address }

// SignCanonical implements signer.Signer. Delegates to the chain-commons
// keystore's Sign method, which produces an EIP-191 personal-sign
// signature — exactly what registry-daemon's manifest signing expects.
//
// Format invariant (preserved): keccak256("\x19Ethereum Signed Message:\n"
// + len(canonical) + canonical), signed with secp256k1, returned as 65
// bytes [r || s || v] with v in {27, 28} (or {0, 1} depending on the
// underlying go-ethereum version's convention — both are valid for EIP-191
// recovery).
func (a *adapter) SignCanonical(canonical []byte) ([]byte, error) {
	return a.ks.Sign(canonical)
}

// Compile-time: adapter satisfies signer.Signer.
var _ signer.Signer = (*adapter)(nil)
