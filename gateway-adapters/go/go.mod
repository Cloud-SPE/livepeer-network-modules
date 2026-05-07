// Go half of gateway-adapters/. Sub-module of the monorepo, matching
// the precedent of payment-daemon/, capability-broker/,
// orch-coordinator/, and secure-orch-console/. Future extraction is a
// mechanical move.
module github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go

go 1.25.0

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../../livepeer-network-protocol/proto-go

require github.com/yutopp/go-rtmp v0.0.7

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.0 // indirect
	github.com/mitchellh/mapstructure v1.4.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/sirupsen/logrus v1.7.0 // indirect
	github.com/yutopp/go-amf0 v0.1.0 // indirect
	golang.org/x/sys v0.3.0 // indirect
)
