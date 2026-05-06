module github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon

go 1.25.0

// In-monorepo dependency on the proto-go module. Until the monorepo
// publishes a v1 tag, the replace directive points at the sibling
// directory; after extraction this becomes a normal versioned dep.
replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../livepeer-network-protocol/proto-go

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go v0.0.0-00010101000000-000000000000
	github.com/ethereum/go-ethereum v1.17.2
	github.com/google/uuid v1.6.0
	go.etcd.io/bbolt v1.4.3
	google.golang.org/grpc v1.81.0
	google.golang.org/protobuf v1.36.11
)

require (
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/bits-and-blooms/bitset v1.20.0 // indirect
	github.com/consensys/gnark-crypto v0.18.1 // indirect
	github.com/crate-crypto/go-eth-kzg v1.5.0 // indirect
	github.com/deckarep/golang-set/v2 v2.6.0 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/ethereum/c-kzg-4844/v2 v2.1.6 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	github.com/supranational/blst v0.3.16 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)
