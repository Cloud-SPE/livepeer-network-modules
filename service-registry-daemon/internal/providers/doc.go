// Package providers defines the cross-cutting interfaces that
// service/ and repo/ code depends on for I/O-bearing capabilities:
// chain reads/writes, manifest HTTP fetching, signing, signature
// verification, time, persistent state, structured logging.
//
// This is the ONLY layer in the repo that may import I/O libraries
// (go-ethereum, BoltDB, net/http). Business logic must reach them
// through these interfaces. See docs/design-docs/architecture.md and
// docs/design-docs/core-beliefs.md §6.
//
// Subpackages each hold one provider's interface plus its
// implementations. The convention is:
//
//	providers/<name>/             — package <name>; defines the interface and provides Default()
//	providers/<name>/<impl>.go    — concrete implementations (e.g. providers/chain/eth.go)
//
// Interfaces that take a leading ctx context.Context are expected to
// make I/O calls. Interfaces without ctx are pure/cached reads.
package providers
