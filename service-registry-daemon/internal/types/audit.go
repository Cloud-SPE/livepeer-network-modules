package types

import "time"

// AuditEvent is one entry in the resolver audit log. Stored in
// providers/store under a per-address bucket and queryable via the
// Resolver.GetAuditLog gRPC.
type AuditEvent struct {
	At         time.Time
	EthAddress EthAddress
	Kind       AuditKind
	Mode       ResolveMode
	Detail     string // free-form, structured-log shape
}

// AuditKind enumerates the events catalogued in
// docs/design-docs/resolver-cache.md.
type AuditKind int

const (
	AuditUnknown AuditKind = iota
	AuditManifestFetched
	AuditManifestUnchanged
	AuditManifestChanged
	AuditSignatureInvalid
	AuditChainURIChanged
	AuditModeChanged
	AuditFallbackUsed
	AuditEvicted
	AuditPublishWritten
	AuditPublishOnchain
)

func (k AuditKind) String() string {
	switch k {
	case AuditManifestFetched:
		return "manifest_fetched"
	case AuditManifestUnchanged:
		return "manifest_unchanged"
	case AuditManifestChanged:
		return "manifest_changed"
	case AuditSignatureInvalid:
		return "signature_invalid"
	case AuditChainURIChanged:
		return "chain_uri_changed"
	case AuditModeChanged:
		return "mode_changed"
	case AuditFallbackUsed:
		return "fallback_used"
	case AuditEvicted:
		return "evicted"
	case AuditPublishWritten:
		return "publish_written"
	case AuditPublishOnchain:
		return "publish_onchain"
	default:
		return "unknown"
	}
}
