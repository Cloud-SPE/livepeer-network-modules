package chaintesting

import "github.com/Cloud-SPE/livepeer-network-modules/chain-commons/providers/store"

// NewFakeStore returns an in-memory store.Store. Convenience alias over
// store.Memory() so test files have a consistent "Fake" naming pattern.
func NewFakeStore() store.Store {
	return store.Memory()
}
