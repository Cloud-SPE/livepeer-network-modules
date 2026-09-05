module github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance

go 1.25.0

require (
	github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify v0.0.0-00010101000000-000000000000
	github.com/ethereum/go-ethereum v1.17.5
	github.com/gorilla/websocket v1.5.3
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.0 // indirect
)

require (
	github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go v0.0.0-00010101000000-000000000000
	github.com/ProjectZKM/Ziren/crates/go-runtime/zkvm_runtime v0.0.0-20251001021608-1fe7b43fc4d6 // indirect
	github.com/decred/dcrd/dcrec/secp256k1/v4 v4.0.1 // indirect
	github.com/holiman/uint256 v1.3.2 // indirect
	golang.org/x/sys v0.42.0 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify => ../verify

replace github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go => ../proto-go
