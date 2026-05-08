// Package protocoldaemon is the root of the protocol-daemon module.
//
// protocol-daemon handles two on-chain orchestrator responsibilities:
// round initialization (RoundsManager.initializeRound) and reward calling
// (BondingManager.rewardWithHint). It is built on the chain-commons
// library; every on-chain write goes through chain-commons.services.txintent.
//
// See README.md and docs/design-docs/architecture.md for the full picture.
package protocoldaemon
