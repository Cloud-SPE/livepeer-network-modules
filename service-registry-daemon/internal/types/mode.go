package types

// ResolveMode is how the resolver interpreted a given on-chain
// serviceURI. See docs/design-docs/serviceuri-modes.md.
type ResolveMode int

const (
	ModeUnknown ResolveMode = iota
	// ModeWellKnown — serviceURI is the full manifest URL to fetch.
	ModeWellKnown
	// ModeCSV — serviceURI is the comma-delimited <url>,<v>,<base64-json>
	// format from the rejected on-chain proposal. Read-only; never produced
	// by this codebase. Manifests in this mode are unsigned.
	ModeCSV
	// ModeLegacy — serviceURI is a plain URL with no manifest available.
	// Resolver synthesizes a single Node{URL:serviceURI}.
	ModeLegacy
	// ModeStaticOverlay — no chain entry exists for the address, but the
	// operator overlay carries an enabled entry with pin nodes. Resolver
	// serves those pins directly. Used by static-overlay-only deployments
	// that want to ignore the chain entirely.
	ModeStaticOverlay
)

func (m ResolveMode) String() string {
	switch m {
	case ModeWellKnown:
		return "well-known"
	case ModeCSV:
		return "csv"
	case ModeLegacy:
		return "legacy"
	case ModeStaticOverlay:
		return "static-overlay"
	default:
		return "unknown"
	}
}

// SignatureStatus categorizes how much trust the resolver placed in a
// node's advertised data.
type SignatureStatus int

const (
	SigUnknown SignatureStatus = iota
	// SigVerified — manifest signature recovered to the chain-claimed
	// eth address. Trustworthy as far as this daemon can tell.
	SigVerified
	// SigUnsigned — content arrived without a verifiable signature
	// (CSV mode, or static-overlay-only). Consumer should apply policy.
	SigUnsigned
	// SigLegacy — content was synthesized from a plain URL serviceURI;
	// no capability data, just a URL to dial.
	SigLegacy
	// SigInvalid — a signature was present but did not recover to the
	// claimed address. The resolver normally rejects these; only present
	// in audit-log entries.
	SigInvalid
)

func (s SignatureStatus) String() string {
	switch s {
	case SigVerified:
		return "signed-verified"
	case SigUnsigned:
		return "unsigned"
	case SigLegacy:
		return "legacy"
	case SigInvalid:
		return "signature-invalid"
	default:
		return "unknown"
	}
}

// FreshnessStatus indicates how stale the cache entry serving a result is.
type FreshnessStatus int

const (
	FreshUnknown FreshnessStatus = iota
	Fresh
	StaleRecoverable
	StaleFailing
)

func (f FreshnessStatus) String() string {
	switch f {
	case Fresh:
		return "fresh"
	case StaleRecoverable:
		return "stale_recoverable"
	case StaleFailing:
		return "stale_failing"
	default:
		return "unknown"
	}
}

// Source distinguishes where a node came from.
type Source int

const (
	SourceUnknown Source = iota
	SourceManifest
	SourceLegacy
	SourceStaticOverlay
	SourceCSVFallback
)

func (s Source) String() string {
	switch s {
	case SourceManifest:
		return "manifest"
	case SourceLegacy:
		return "legacy"
	case SourceStaticOverlay:
		return "static-overlay"
	case SourceCSVFallback:
		return "csv-fallback"
	default:
		return "unknown"
	}
}
