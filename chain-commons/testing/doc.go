// Package testing provides fakes for every chain-commons provider so that
// downstream daemon test suites don't dial real RPC, write to real BoltDB
// files, or import sensitive keystore content.
//
// All fakes are concurrency-safe; the daemon test code can run them under
// `go test -race` and trust the result.
//
// Imported by payment-daemon, service-registry-daemon, protocol-daemon test
// suites. Public API; breaking-change rules apply.
//
// This package's name is "chaintesting" so consumers can write
// `import chaintest "github.com/Cloud-SPE/livepeer-network-rewrite/chain-commons/testing"`
// without colliding with the stdlib testing package.
package chaintesting
