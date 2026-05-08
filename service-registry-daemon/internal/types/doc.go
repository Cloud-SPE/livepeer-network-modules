// Package types holds the pure data primitives of the registry: Manifest,
// Node, Capability, Model, ResolveMode, EthAddress, and the canonical
// errors. It depends on nothing in internal/ and on no I/O-bearing
// libraries — only stdlib and the keccak256 + secp256k1 primitives we
// need for canonical-bytes hashing and signature recovery.
//
// The wire-format guarantees of the manifest (JSON canonicalization,
// signature digest) live here. See docs/design-docs/manifest-schema.md
// and docs/design-docs/signature-scheme.md.
package types
