// Package chaincommons is the shared chain-glue library for the
// livepeer-modules-project monorepo.
//
// It provides the Ethereum/Arbitrum interaction primitives that
// payment-daemon, service-registry-daemon, and protocol-daemon all
// consume: multi-RPC failover, durable transaction state, Controller-
// resolved sub-contract addresses, gas oracle, log subscriptions with
// durable offsets, reorg-aware confirmation tracking, keystore signing,
// BoltDB persistence, structured logging, and Prometheus-recordable
// metrics (via a Recorder interface — no direct Prometheus dependency).
//
// chain-commons is a library, never a daemon. It has no cmd/, no main,
// and no Docker image. It is consumed by daemons.
//
// See docs/design-docs/chain-commons-api.md in the monorepo for the full
// API surface; docs/design-docs/{tx-intent-state-machine,
// multi-rpc-failover, controller-resolver, event-log-offsets}.md cover
// individual subsystems.
package chaincommons
