module github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator

go 1.25.0

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify v0.0.0-00010101000000-000000000000
	github.com/ethereum/go-ethereum v1.17.2
	go.etcd.io/bbolt v1.4.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	golang.org/x/sys v0.40.0 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify => ../livepeer-network-protocol/verify
