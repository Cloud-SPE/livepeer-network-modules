// Package utils holds zero-dependency helpers shared across layers.
// Anything here must be pure (no I/O, no goroutines), depend only on
// stdlib, and be small enough to read in one sitting.
//
// Things that don't fit (anything with state, anything with a
// dependency, anything > ~50 lines) belong in a more specific
// package — typically a provider.
package utils
