// Package verify holds the cross-cutting manifest-signature verifier.
// Resolvers, coordinators, and gateways all consume signed manifests
// and re-run the same secp256k1 + EIP-191 personal-sign recovery; this
// module is the single source of truth for that recovery so the bytes-
// identical guarantee with secure-orch-console's signer holds.
module github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify

go 1.25.0

require github.com/ethereum/go-ethereum v1.17.2

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	golang.org/x/sys v0.40.0 // indirect
)
