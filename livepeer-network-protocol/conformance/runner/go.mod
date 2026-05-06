module github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner

go 1.25.0

require gopkg.in/yaml.v3 v3.0.1

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go v0.0.0-00010101000000-000000000000
	github.com/gorilla/websocket v1.5.3
	google.golang.org/protobuf v1.36.11
)

require (
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
	google.golang.org/grpc v1.81.0 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../../proto-go
