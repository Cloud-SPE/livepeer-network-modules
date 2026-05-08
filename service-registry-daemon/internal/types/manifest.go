package types

import (
	"encoding/json"
	"time"
)

// SchemaVersion is the manifest schema version this codebase produces.
// Validators accept the caret-compatible range rooted at this version:
// >= 3.0.1 and < 4.0.0.
const SchemaVersion = "3.0.1"

// SignatureAlgEthPersonal is the only signature algorithm v3 accepts.
const SignatureAlgEthPersonal = "eth-personal-sign"

// Manifest is the JSON document an operator publishes at the exact URL
// returned by getServiceURI(). The struct tags produce the canonical
// key set; field order at marshal time is fixed by the canonical-bytes
// procedure (see CanonicalBytes).
//
// Field-level conventions:
//   - SchemaVersion: required, currently always "3.0.1".
//   - EthAddress: required, lower-cased 0x-prefixed.
//   - IssuedAt: required, RFC3339 UTC.
//   - Nodes: required, non-empty.
//   - Signature: filled in by the publisher; zero-valued before signing.
type Manifest struct {
	SchemaVersion string    `json:"schema_version"`
	EthAddress    string    `json:"eth_address"`
	IssuedAt      time.Time `json:"issued_at"`
	Nodes         []Node    `json:"nodes"`
	Signature     Signature `json:"signature"`
}

// Signature carries the eth-personal-sign output plus a diagnostic hash.
type Signature struct {
	Alg                        string `json:"alg"`
	Value                      string `json:"value"`                         // 0x-prefixed 130-hex
	SignedCanonicalBytesSHA256 string `json:"signed_canonical_bytes_sha256"` // 0x-prefixed 64-hex
}

// Node describes one orchestrator endpoint within a manifest.
type Node struct {
	ID               string          `json:"id"`
	URL              string          `json:"url"`
	WorkerEthAddress string          `json:"worker_eth_address,omitempty"`
	Extra            json.RawMessage `json:"extra,omitempty"`
	Capabilities     []Capability    `json:"capabilities"`
}

// Capability advertises one named operation. Name is opaque to this
// daemon — see docs/design-docs/workload-agnostic-strings.md.
type Capability struct {
	Name      string          `json:"name"`
	WorkUnit  string          `json:"work_unit,omitempty"`
	Offerings []Offering      `json:"offerings,omitempty"`
	Extra     json.RawMessage `json:"extra,omitempty"`
}

// Offering is a priced tier under a capability. The ID is opaque — for
// AI workloads it's typically the model name (e.g. "gpt-oss-20b"); for
// video transcoding a preset id (e.g. "h264-1080p"); for streaming
// sessions a resolution/fps tier (e.g. "vtuber-1080p30"). Pricing is
// the per-work-unit wholesale rate the orchestrator advertises;
// gateways/bridges read it as the wholesale-side input to routing.
type Offering struct {
	ID                  string          `json:"id"`
	PricePerWorkUnitWei string          `json:"price_per_work_unit_wei,omitempty"` // decimal big-int as string
	Constraints         json.RawMessage `json:"constraints,omitempty"`
}

// Clone returns a deep copy of the manifest. Used in canonicalization
// (we zero the Signature for hashing without mutating the input).
func (m *Manifest) Clone() *Manifest {
	out := *m
	if len(m.Nodes) > 0 {
		out.Nodes = make([]Node, len(m.Nodes))
		copy(out.Nodes, m.Nodes)
		for i := range out.Nodes {
			if len(m.Nodes[i].Extra) > 0 {
				out.Nodes[i].Extra = append([]byte(nil), m.Nodes[i].Extra...)
			}
			if len(m.Nodes[i].Capabilities) > 0 {
				out.Nodes[i].Capabilities = make([]Capability, len(m.Nodes[i].Capabilities))
				copy(out.Nodes[i].Capabilities, m.Nodes[i].Capabilities)
				for j := range out.Nodes[i].Capabilities {
					if len(m.Nodes[i].Capabilities[j].Extra) > 0 {
						out.Nodes[i].Capabilities[j].Extra = append([]byte(nil), m.Nodes[i].Capabilities[j].Extra...)
					}
					if len(m.Nodes[i].Capabilities[j].Offerings) > 0 {
						out.Nodes[i].Capabilities[j].Offerings = make([]Offering, len(m.Nodes[i].Capabilities[j].Offerings))
						copy(out.Nodes[i].Capabilities[j].Offerings, m.Nodes[i].Capabilities[j].Offerings)
						for k := range out.Nodes[i].Capabilities[j].Offerings {
							if len(m.Nodes[i].Capabilities[j].Offerings[k].Constraints) > 0 {
								out.Nodes[i].Capabilities[j].Offerings[k].Constraints = append([]byte(nil), m.Nodes[i].Capabilities[j].Offerings[k].Constraints...)
							}
						}
					}
				}
			}
		}
	}
	return &out
}
