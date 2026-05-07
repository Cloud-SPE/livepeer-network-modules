// Go half of gateway-adapters/. Sub-module of the monorepo, matching
// the precedent of payment-daemon/, capability-broker/,
// orch-coordinator/, and secure-orch-console/. Future extraction is a
// mechanical move.
module github.com/Cloud-SPE/livepeer-network-rewrite/gateway-adapters/go

go 1.25.0

replace github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go => ../../livepeer-network-protocol/proto-go
