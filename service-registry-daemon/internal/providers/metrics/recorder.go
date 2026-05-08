package metrics

import (
	"net/http"
	"time"
)

// Recorder is the single metrics surface. Every domain emits through
// it; implementations decide how to record. Two implementations:
//
//   - Prometheus (production): writes to a Prometheus *Registry,
//     enforces a cardinality cap per metric, exposes the standard
//     /metrics handler.
//   - Noop (default when --metrics-listen is unset): zero-cost no-op,
//     Handler returns 404.
//
// Method ordering follows the catalog in docs/design-docs/
// observability.md. New emissions add a method here and wire it in
// every implementation.
type Recorder interface {
	// ----- gRPC -----

	// IncGRPCRequest counts one completed gRPC request. registryCode
	// is the stable application-level code from
	// docs/product-specs/grpc-surface.md (or "" on success). Empty
	// labels are emitted as "_unset_" to avoid Prom rejecting empty
	// strings.
	IncGRPCRequest(service, method, code, registryCode string)
	// ObserveGRPC records the unary handler latency. Two histograms
	// fire: a coarse-grained one (default Prom buckets) and a fine-
	// grained sub-ms variant for the unix-socket fast path.
	ObserveGRPC(service, method string, d time.Duration)
	// SetGRPCInFlight reports the current count of in-flight requests
	// for (service, method).
	SetGRPCInFlight(service, method string, n int)

	// ----- Resolver flow -----

	// IncResolution counts one terminal resolve outcome.
	IncResolution(mode, freshness string)
	// ObserveResolveDuration is the end-to-end resolve latency
	// including chain read + manifest fetch + verification + overlay
	// merge. NOT just the cached-hit fast path.
	ObserveResolveDuration(mode, freshness string, d time.Duration)
	// IncLegacyFallback counts the times the resolver synthesized a
	// legacy node because a manifest was unavailable / too large /
	// unparseable.
	IncLegacyFallback(reason string)

	// ----- Manifest pipeline -----

	IncManifestFetch(outcome string)
	ObserveManifestFetch(outcome string, d time.Duration, bodyBytes int)
	IncManifestVerify(outcome string)
	ObserveSignatureVerify(d time.Duration)

	// ----- Cache + audit -----

	IncCacheLookup(result string) // hit_fresh | hit_stale | miss
	IncCacheWrite()
	IncCacheEviction(reason string)
	SetCacheEntries(n int)
	IncAudit(kind string)

	// ----- Overlay -----

	IncOverlayReload(outcome string)
	SetOverlayEntries(n int)
	IncOverlayDrop(reason string)

	// ----- Chain provider -----

	IncChainRead(outcome string)
	IncChainWrite(outcome string)
	ObserveChainRead(d time.Duration)
	SetChainLastSuccess(t time.Time)
	SetManifestFetcherLastSuccess(t time.Time)

	// ----- Publisher -----

	IncPublisherBuild()
	IncPublisherSign(outcome string)
	IncPublisherProbe(outcome string)

	// ----- Daemon-level -----

	SetUptimeSeconds(s float64)
	SetBuildInfo(version, mode, goVersion string)

	// ----- Exposition -----

	// Handler returns the http.Handler that serves the Prometheus
	// exposition format on the metrics listener. For Noop it returns
	// 404, so an operator who forgets `--metrics-listen` and points
	// Prometheus at a stale port gets a clear "no metrics here"
	// signal rather than a successful empty scrape.
	Handler() http.Handler
}

// Sentinels for label values. Use these constants instead of string
// literals at call sites so a typo fails to compile.
const (
	// freshness
	FreshnessFresh            = "fresh"
	FreshnessStaleRecoverable = "stale_recoverable"
	FreshnessStaleFailing     = "stale_failing"

	// mode
	ModeWellKnown     = "well_known"
	ModeCSV           = "csv"
	ModeLegacy        = "legacy"
	ModeStaticOverlay = "static_overlay"
	ModeUnknown       = "unknown"

	// resolve outcome / verify outcome
	OutcomeOK                 = "ok"
	OutcomeNotFound           = "not_found"
	OutcomeUnavailable        = "unavailable"
	OutcomeReverted           = "reverted"
	OutcomeNotImplemented     = "not_implemented"
	OutcomeDisallowed         = "disallowed"
	OutcomeTooLarge           = "too_large"
	OutcomeHTTPError          = "http_error"
	OutcomeTimeout            = "timeout"
	OutcomeVerified           = "verified"
	OutcomeSignatureMismatch  = "signature_mismatch"
	OutcomeParseError         = "parse_error"
	OutcomeExpired            = "expired"
	OutcomeEthAddressMismatch = "eth_address_mismatch"
	OutcomeKeystoreLocked     = "keystore_locked"
	OutcomeIOError            = "io_error"

	// cache lookup result
	CacheHitFresh = "hit_fresh"
	CacheHitStale = "hit_stale"
	CacheMiss     = "miss"

	// eviction reason
	EvictChainURIChanged = "chain_uri_changed"
	EvictForced          = "forced"
	EvictMaxStale        = "max_stale"

	// overlay drop reason
	OverlayDropSignaturePolicy = "signature_policy"
	OverlayDropDisabled        = "disabled"
	OverlayDropTierFilter      = "tier_filter"

	// audit kinds — match types.AuditKind.String()
	// (we do not import types here to keep this package zero-
	// dependency from the rest of internal/; the contract is enforced
	// by tests in audit_test.go using types.AuditKind.String()).

	// label fallback for unset values
	LabelUnset = "_unset_"
)
