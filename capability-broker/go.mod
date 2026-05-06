module github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker

go 1.25.0

require (
	github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go v0.0.0-00010101000000-000000000000
	github.com/gorilla/websocket v1.5.3
	github.com/prometheus/client_golang v1.23.2
	google.golang.org/grpc v1.81.0
	google.golang.org/protobuf v1.36.11
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/net v0.51.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260226221140-a57be14db171 // indirect
)

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../livepeer-network-protocol/proto-go
