// Package diff produces a structural diff of a candidate manifest
// against the last successfully signed manifest. The diff is keyed on
// (capability_id, offering_id) tuples per plan 0019 §6.2.
//
// Today this package exposes a minimal Compute API used by the
// console's web handler stub. The full diff renderer (templates,
// expand-collapse, side-by-side rendering) lives in commit 5.
package diff

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Result is the structural diff outcome.
type Result struct {
	Added     []Tuple        `json:"added"`
	Removed   []Tuple        `json:"removed"`
	Changed   []ChangedTuple `json:"changed"`
	Unchanged []Tuple        `json:"unchanged"`
	Header    HeaderChange   `json:"header"`
}

// Tuple is a flat capability tuple, identified by (capability_id,
// offering_id).
type Tuple struct {
	CapabilityID string         `json:"capability_id"`
	OfferingID   string         `json:"offering_id"`
	Fields       map[string]any `json:"fields"`
}

// ChangedTuple is a tuple whose (capability_id, offering_id) is the
// same on both sides but at least one other field differs.
type ChangedTuple struct {
	CapabilityID string         `json:"capability_id"`
	OfferingID   string         `json:"offering_id"`
	Before       map[string]any `json:"before"`
	After        map[string]any `json:"after"`
}

// HeaderChange surfaces top-of-screen metadata. The diff highlights
// publication_seq monotonicity and orch.eth_address stability per
// plan 0019 §6.2.
type HeaderChange struct {
	BeforeSeq        *uint64 `json:"before_publication_seq,omitempty"`
	AfterSeq         uint64  `json:"after_publication_seq"`
	SeqMonotonic     bool    `json:"publication_seq_monotonic"`
	BeforeEthAddress string  `json:"before_eth_address,omitempty"`
	AfterEthAddress  string  `json:"after_eth_address"`
	EthAddressStable bool    `json:"eth_address_stable"`
	BeforeIssuedAt   string  `json:"before_issued_at,omitempty"`
	AfterIssuedAt    string  `json:"after_issued_at"`
	BeforeExpiresAt  string  `json:"before_expires_at,omitempty"`
	AfterExpiresAt   string  `json:"after_expires_at"`
}

// Compute computes the structural diff between the inner manifest
// payloads of two envelopes. before may be nil (first sign cycle).
func Compute(beforeEnvelope, afterEnvelope []byte) (*Result, error) {
	if afterEnvelope == nil {
		return nil, errors.New("diff: candidate envelope is required")
	}
	after, err := extractManifest(afterEnvelope)
	if err != nil {
		return nil, fmt.Errorf("diff: candidate: %w", err)
	}
	var before map[string]any
	if beforeEnvelope != nil {
		before, err = extractManifest(beforeEnvelope)
		if err != nil {
			return nil, fmt.Errorf("diff: last-signed: %w", err)
		}
	}
	return diff(before, after), nil
}

func extractManifest(envelope []byte) (map[string]any, error) {
	var outer map[string]any
	if err := json.Unmarshal(envelope, &outer); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}
	m, ok := outer["manifest"].(map[string]any)
	if ok {
		return m, nil
	}
	// Candidates from orch-coordinator arrive as bare manifest.json,
	// while last-signed on disk is wrapped as {manifest, signature}.
	if _, hasSpec := outer["spec_version"]; hasSpec {
		return outer, nil
	}
	return nil, errors.New("envelope missing manifest object")
}

func diff(before, after map[string]any) *Result {
	res := &Result{Header: header(before, after)}
	beforeMap := index(before)
	afterMap := index(after)
	keys := make([]tupleKey, 0, len(beforeMap)+len(afterMap))
	seen := map[tupleKey]bool{}
	for k := range beforeMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range afterMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].cap != keys[j].cap {
			return keys[i].cap < keys[j].cap
		}
		return keys[i].off < keys[j].off
	})
	for _, k := range keys {
		b, hasB := beforeMap[k]
		a, hasA := afterMap[k]
		switch {
		case !hasB && hasA:
			res.Added = append(res.Added, Tuple{CapabilityID: k.cap, OfferingID: k.off, Fields: a})
		case hasB && !hasA:
			res.Removed = append(res.Removed, Tuple{CapabilityID: k.cap, OfferingID: k.off, Fields: b})
		case fieldsEqual(b, a):
			res.Unchanged = append(res.Unchanged, Tuple{CapabilityID: k.cap, OfferingID: k.off, Fields: a})
		default:
			res.Changed = append(res.Changed, ChangedTuple{CapabilityID: k.cap, OfferingID: k.off, Before: b, After: a})
		}
	}
	return res
}

type tupleKey struct {
	cap, off string
}

func index(m map[string]any) map[tupleKey]map[string]any {
	out := map[tupleKey]map[string]any{}
	if m == nil {
		return out
	}
	caps, ok := m["capabilities"].([]any)
	if !ok {
		return out
	}
	for _, raw := range caps {
		c, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		cap, _ := c["capability_id"].(string)
		off, _ := c["offering_id"].(string)
		if cap == "" {
			continue
		}
		out[tupleKey{cap: cap, off: off}] = c
	}
	return out
}

func header(before, after map[string]any) HeaderChange {
	h := HeaderChange{}
	if after != nil {
		h.AfterSeq = readUint64(after, "publication_seq")
		h.AfterEthAddress = readEthAddress(after)
		h.AfterIssuedAt, _ = after["issued_at"].(string)
		h.AfterExpiresAt, _ = after["expires_at"].(string)
	}
	if before != nil {
		seq := readUint64(before, "publication_seq")
		h.BeforeSeq = &seq
		h.BeforeEthAddress = readEthAddress(before)
		h.BeforeIssuedAt, _ = before["issued_at"].(string)
		h.BeforeExpiresAt, _ = before["expires_at"].(string)
		h.SeqMonotonic = h.AfterSeq > seq
		h.EthAddressStable = h.AfterEthAddress == h.BeforeEthAddress
	} else {
		h.SeqMonotonic = true
		h.EthAddressStable = true
	}
	return h
}

func readUint64(m map[string]any, key string) uint64 {
	switch v := m[key].(type) {
	case float64:
		if v < 0 {
			return 0
		}
		return uint64(v)
	case json.Number:
		n, err := v.Int64()
		if err != nil || n < 0 {
			return 0
		}
		return uint64(n)
	default:
		return 0
	}
}

func readEthAddress(m map[string]any) string {
	orch, ok := m["orch"].(map[string]any)
	if !ok {
		return ""
	}
	addr, _ := orch["eth_address"].(string)
	return addr
}

func fieldsEqual(a, b map[string]any) bool {
	ab, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	return string(ab) == string(bb)
}
