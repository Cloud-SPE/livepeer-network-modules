package candidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

var openAICapabilityPrefixes = []string{
	"openai:chat-completions:",
	"openai:embeddings:",
	"openai:audio-transcriptions:",
	"openai:audio-speech:",
	"openai:images-generations:",
	"openai:realtime:",
}

// SpecVersion is the manifest spec version emitted by this coordinator.
// Pinned to whatever the spec repo declares. Update in lockstep with
// livepeer-network-protocol/manifest/changelog.md.
const SpecVersion = "0.1.0"

// PriceConflictError is returned when two brokers advertise the same
// uniqueness key with different prices. Hard fail loud — the candidate
// build refuses to proceed; the operator must reconcile the broker
// configuration.
type PriceConflictError struct {
	CapabilityID string
	OfferingID   string
	Extra        map[string]any
	Constraints  map[string]any
	A            ConflictParty
	B            ConflictParty
}

// ConflictParty is one side of a price conflict.
type ConflictParty struct {
	BrokerName string
	WorkerURL  string
	Price      string
}

func (e *PriceConflictError) Error() string {
	return fmt.Sprintf("candidate: price conflict on (%s, %s): broker %s @ %s vs broker %s @ %s",
		e.CapabilityID, e.OfferingID,
		e.A.BrokerName, e.A.Price,
		e.B.BrokerName, e.B.Price)
}

// BuildOptions controls candidate-build behavior.
type BuildOptions struct {
	OrchEthAddress    string
	ServiceURI        string
	ManifestTTL       time.Duration
	PublicationSeq    uint64
	CoordinatorCommit string
}

// Build assembles a candidate from a scrape snapshot. The result is
// idempotent: same snapshot + same options → byte-identical
// manifest.json.
func Build(snap scrape.Snapshot, opts BuildOptions) (*types.Candidate, error) {
	if opts.OrchEthAddress == "" {
		return nil, errors.New("candidate: orch eth address required")
	}
	if opts.ManifestTTL <= 0 {
		return nil, errors.New("candidate: manifest_ttl must be positive")
	}

	tuples, ha, err := aggregate(snap.SourceTuples)
	if err != nil {
		return nil, err
	}

	issuedAt := snap.WindowEnd.UTC()
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	expiresAt := issuedAt.Add(opts.ManifestTTL)

	payload := types.ManifestPayload{
		SpecVersion:    SpecVersion,
		PublicationSeq: opts.PublicationSeq,
		IssuedAt:       issuedAt,
		ExpiresAt:      expiresAt,
		Orch: types.Orch{
			EthAddress: strings.ToLower(strings.TrimSpace(opts.OrchEthAddress)),
			ServiceURI: opts.ServiceURI,
		},
		Capabilities: tuples,
	}

	bytes, err := canonicalManifestBytes(payload)
	if err != nil {
		return nil, err
	}

	meta := types.Metadata{
		CandidateTimestamp: issuedAt,
		ScrapeWindowStart:  snap.WindowStart.UTC(),
		ScrapeWindowEnd:    snap.WindowEnd.UTC(),
		SourceBrokers:      brokerEntries(snap),
		CoordinatorCommit:  opts.CoordinatorCommit,
		SchemaVersion:      SpecVersion,
		HAEndpoints:        ha,
	}

	return &types.Candidate{
		ManifestBytes: bytes,
		Manifest:      payload,
		Metadata:      meta,
	}, nil
}

func canonicalManifestBytes(p types.ManifestPayload) ([]byte, error) {
	// Hand-build the manifest object so the JSON shape exactly matches
	// the schema (no Go-zero-value leakage). We marshal through
	// CanonicalBytes so RFC 8785 key ordering is enforced regardless
	// of struct field order.
	root := map[string]any{
		"spec_version":    p.SpecVersion,
		"publication_seq": p.PublicationSeq,
		"issued_at":       p.IssuedAt.UTC().Format(time.RFC3339Nano),
		"expires_at":      p.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"orch":            orchToMap(p.Orch),
		"capabilities":    capsToList(p.Capabilities),
	}
	return CanonicalBytes(root)
}

func orchToMap(o types.Orch) map[string]any {
	m := map[string]any{"eth_address": o.EthAddress}
	if o.ServiceURI != "" {
		m["service_uri"] = o.ServiceURI
	}
	return m
}

func capsToList(caps []types.CapabilityTuple) []any {
	out := make([]any, 0, len(caps))
	for _, c := range caps {
		entry := map[string]any{
			"capability_id":      c.CapabilityID,
			"offering_id":        c.OfferingID,
			"interaction_mode":   c.InteractionMode,
			"work_unit":          map[string]any{"name": c.WorkUnit.Name},
			"price_per_unit_wei": c.PricePerUnitWei,
			"worker_url":         c.WorkerURL,
		}
		if len(c.Extra) > 0 {
			entry["extra"] = c.Extra
		}
		if len(c.Constraints) > 0 {
			entry["constraints"] = c.Constraints
		}
		out = append(out, entry)
	}
	return out
}

// aggregate folds source tuples into the manifest's capability list
// per the §5 / Q2 dedup rules. Returns the sorted tuple list, the
// HA-endpoint sidecar, or a PriceConflictError on identical-key-
// different-prices.
func aggregate(sources []types.SourceTuple) ([]types.CapabilityTuple, []types.HAEndpoint, error) {
	type group struct {
		// representative tuple (lex-min worker_url for the group)
		rep types.CapabilityTuple
		// brokers contributing the same key+price
		brokers []types.SourceTuple
	}
	keyed := make(map[string]*group)
	keyOrder := make([]string, 0)

	for _, s := range sources {
		key, err := uniquenessKey(s.Offering)
		if err != nil {
			return nil, nil, fmt.Errorf("candidate: %w (broker=%s)", err, s.BrokerName)
		}
		g, ok := keyed[key]
		if !ok {
			g = &group{rep: tupleFrom(s)}
			keyed[key] = g
			keyOrder = append(keyOrder, key)
			g.brokers = append(g.brokers, s)
			continue
		}
		// Same key already exists. Price must match.
		if g.rep.PricePerUnitWei != s.Offering.PricePerUnitWei {
			return nil, nil, &PriceConflictError{
				CapabilityID: s.Offering.CapabilityID,
				OfferingID:   s.Offering.OfferingID,
				Extra:        s.Offering.Extra,
				Constraints:  s.Offering.Constraints,
				A: ConflictParty{
					BrokerName: g.brokers[0].BrokerName,
					WorkerURL:  g.brokers[0].WorkerURL,
					Price:      g.rep.PricePerUnitWei,
				},
				B: ConflictParty{
					BrokerName: s.BrokerName,
					WorkerURL:  s.WorkerURL,
					Price:      s.Offering.PricePerUnitWei,
				},
			}
		}
		// HA pair: same key + same price, different worker_url.
		if g.rep.WorkerURL != s.WorkerURL && s.WorkerURL < g.rep.WorkerURL {
			g.rep.WorkerURL = s.WorkerURL
		}
		g.brokers = append(g.brokers, s)
	}

	tuples := make([]types.CapabilityTuple, 0, len(keyed))
	ha := make([]types.HAEndpoint, 0)
	for _, key := range keyOrder {
		g := keyed[key]
		tuples = append(tuples, g.rep)
		if len(g.brokers) > 1 {
			for _, src := range g.brokers {
				if src.WorkerURL == g.rep.WorkerURL {
					continue
				}
				ha = append(ha, types.HAEndpoint{
					CapabilityID: g.rep.CapabilityID,
					OfferingID:   g.rep.OfferingID,
					PrimaryURL:   g.rep.WorkerURL,
					AlternateURL: src.WorkerURL,
					BrokerName:   src.BrokerName,
				})
			}
		}
	}

	sort.Slice(tuples, func(i, j int) bool {
		a, b := tuples[i], tuples[j]
		if a.CapabilityID != b.CapabilityID {
			return a.CapabilityID < b.CapabilityID
		}
		if a.OfferingID != b.OfferingID {
			return a.OfferingID < b.OfferingID
		}
		return a.WorkerURL < b.WorkerURL
	})
	sort.Slice(ha, func(i, j int) bool {
		a, b := ha[i], ha[j]
		if a.CapabilityID != b.CapabilityID {
			return a.CapabilityID < b.CapabilityID
		}
		if a.OfferingID != b.OfferingID {
			return a.OfferingID < b.OfferingID
		}
		return a.AlternateURL < b.AlternateURL
	})
	return tuples, ha, nil
}

// uniquenessKey returns a stable canonical-byte string over
// (capability_id, offering_id, extra, constraints). Worker URL is not
// part of identity (Q2 lock).
func uniquenessKey(o types.BrokerOffering) (string, error) {
	o = normalizeOpenAIOffering(o)
	root := map[string]any{
		"capability_id": o.CapabilityID,
		"offering_id":   o.OfferingID,
	}
	if len(o.Extra) > 0 {
		root["extra"] = o.Extra
	}
	if len(o.Constraints) > 0 {
		root["constraints"] = o.Constraints
	}
	b, err := CanonicalBytes(root)
	if err != nil {
		return "", fmt.Errorf("uniqueness key: %w", err)
	}
	return string(b), nil
}

func tupleFrom(s types.SourceTuple) types.CapabilityTuple {
	offering := normalizeOpenAIOffering(s.Offering)
	return types.CapabilityTuple{
		CapabilityID:    offering.CapabilityID,
		OfferingID:      offering.OfferingID,
		InteractionMode: offering.InteractionMode,
		WorkUnit:        offering.WorkUnit,
		PricePerUnitWei: offering.PricePerUnitWei,
		WorkerURL:       s.WorkerURL,
		Extra:           offering.Extra,
		Constraints:     offering.Constraints,
	}
}

func normalizeOpenAIOffering(o types.BrokerOffering) types.BrokerOffering {
	normalizedID, model := normalizeOpenAICapabilityID(o.CapabilityID)
	if normalizedID == o.CapabilityID && model == "" {
		return o
	}
	o.CapabilityID = normalizedID
	if model == "" {
		return o
	}
	if o.Extra == nil {
		o.Extra = map[string]any{}
	}
	openaiExtra, _ := o.Extra["openai"].(map[string]any)
	if openaiExtra == nil {
		openaiExtra = map[string]any{}
	}
	if raw := strings.TrimSpace(fmt.Sprint(openaiExtra["model"])); raw == "" || raw == "<nil>" {
		openaiExtra["model"] = model
	}
	o.Extra["openai"] = openaiExtra
	return o
}

func normalizeOpenAICapabilityID(capabilityID string) (normalized string, model string) {
	for _, prefix := range openAICapabilityPrefixes {
		if strings.HasPrefix(capabilityID, prefix) {
			suffix := strings.TrimSpace(strings.TrimPrefix(capabilityID, prefix))
			if suffix == "" {
				return strings.TrimSuffix(prefix, ":"), ""
			}
			return strings.TrimSuffix(prefix, ":"), suffix
		}
	}
	return capabilityID, ""
}

func brokerEntries(snap scrape.Snapshot) []types.MetadataBrokerEntry {
	out := make([]types.MetadataBrokerEntry, 0, len(snap.Brokers))
	for _, b := range snap.Brokers {
		out = append(out, types.MetadataBrokerEntry{
			Name:      b.Name,
			BaseURL:   b.BaseURL,
			Status:    b.Freshness,
			ScrapedAt: b.LastSuccessAt,
			Error:     b.LastError,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MarshalMetadata returns the canonical metadata.json bytes (NOT
// signed). Used by the tarball packager.
func MarshalMetadata(m types.Metadata) ([]byte, error) {
	return json.MarshalIndent(m, "", "  ")
}
