module github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon

go 1.25.0

// In-monorepo dependency on the proto-go module. Until the monorepo
// publishes a v1 tag, the replace directive points at the sibling
// directory; after extraction this becomes a normal versioned dep.
replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../livepeer-network-protocol/proto-go

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go v0.0.0-00010101000000-000000000000
	go.etcd.io/bbolt v1.4.3
	google.golang.org/grpc v1.81.0
)

require (
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
)
