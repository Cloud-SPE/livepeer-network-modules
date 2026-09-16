module github.com/Cloud-SPE/livepeer-network-modules/e2e

go 1.25.7

require (
	github.com/Cloud-SPE/livepeer-network-modules/pool-commons v0.0.0
	github.com/Cloud-SPE/livepeer-network-modules/proto-contracts v0.0.0
	github.com/ethereum/go-ethereum v1.17.5
	github.com/gorilla/websocket v1.5.3
	google.golang.org/grpc v1.80.0
)

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/bits-and-blooms/bitset v1.20.0 // indirect
	github.com/consensys/gnark-crypto v0.18.1 // indirect
	github.com/crate-crypto/go-eth-kzg v1.5.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/ethereum/c-kzg-4844/v2 v2.1.8 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/supranational/blst v0.3.16 // indirect
	golang.org/x/net v0.50.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260209200024-4cfbd4190f57 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-modules/pool-commons => ../pool-commons

replace github.com/Cloud-SPE/livepeer-network-modules/proto-contracts => ../proto-contracts
