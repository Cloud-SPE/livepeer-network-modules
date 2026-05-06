module github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator

go 1.25.0

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify v0.0.0-00010101000000-000000000000
	github.com/ethereum/go-ethereum v1.17.2
	github.com/prometheus/client_golang v1.15.0
	go.etcd.io/bbolt v1.4.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/matttproud/golang_protobuf_extensions v1.0.4 // indirect
	github.com/prometheus/client_model v0.3.0 // indirect
	github.com/prometheus/common v0.42.0 // indirect
	github.com/prometheus/procfs v0.9.0 // indirect
	golang.org/x/sys v0.40.0 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/verify => ../livepeer-network-protocol/verify
