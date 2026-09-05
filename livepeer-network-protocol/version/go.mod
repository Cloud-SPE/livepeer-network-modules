// Package version is the single Go source for the spec-wide protocol
// version. Dependency-free on purpose: the broker, coordinator, and
// registry daemon import it to stamp and gate spec_version, and none of
// them should pull anything else in to do so.
module github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version

go 1.25.0
