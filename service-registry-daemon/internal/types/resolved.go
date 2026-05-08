package types

import "time"

// ResolvedNode is what the resolver returns to consumers. It has more
// fields than Node — the extras carry resolver-side decisions (source,
// signature trust, overlay policy) that consumers route on.
type ResolvedNode struct {
	// From the manifest (or legacy synthesis):
	ID               string
	URL              string
	Lat              *float64
	Lon              *float64
	Region           string
	WorkerEthAddress string
	Extra            []byte
	Capabilities     []Capability

	// Resolver-attached:
	Source          Source
	SignatureStatus SignatureStatus
	OperatorAddr    EthAddress // chain-claimed orchestrator identity
	Enabled         bool       // overlay-applied
	TierAllowed     []string   // overlay-applied; nil means no tier filter
	Weight          int        // overlay-applied; default 100
}

// ResolveResult is the full Resolve output: meta + nodes.
type ResolveResult struct {
	EthAddress      EthAddress
	ResolvedURI     string
	Mode            ResolveMode
	Nodes           []ResolvedNode
	FreshnessStatus FreshnessStatus
	CachedAt        time.Time
	FetchedAt       time.Time
	Manifest        *Manifest // nil for legacy mode
	SchemaVersion   string
}
