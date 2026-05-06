// Package diff computes the structural difference between a
// candidate manifest and the currently-published manifest, keyed by
// the §5 / Q2 uniqueness key (capability_id, offering_id, extra,
// constraints). Drift is reported per-row so the roster UX can flag
// which capability tuples the next publish would change.
//
// The diff is advisory — it short-circuits no-op trips to the cold
// key. The authoritative diff (candidate vs last-signed) lives on
// secure-orch-console; that one's what the cold key acts on.
package diff

import (
	"sort"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// Drift kinds. The kind enum is stable for Prometheus labels.
const (
	DriftNone          = "none"
	DriftAdded         = "added"
	DriftRemoved       = "removed"
	DriftPriceChanged  = "price_changed"
	DriftModeChanged   = "mode_changed"
	DriftExtraChanged  = "extra_changed"
	DriftWorkerChanged = "worker_changed"
)

// Row is one diff entry, keyed by the canonicalized uniqueness key.
type Row struct {
	Key             string
	CapabilityID    string
	OfferingID      string
	Drift           string
	Candidate       *types.CapabilityTuple
	Published       *types.CapabilityTuple
	OldPriceWei     string
	NewPriceWei     string
	OldMode         string
	NewMode         string
	OldWorkerURL    string
	NewWorkerURL    string
}

// Result aggregates all rows plus per-kind counts useful for metrics
// and the diff badge.
type Result struct {
	Rows   []Row
	Counts map[string]int
}

// Compute returns the diff between the candidate and the published
// manifest. Either side may be nil — a nil candidate is treated as
// "no proposed-next-version" (everything published is "removed"), and
// a nil published manifest is treated as a fresh deployment
// (everything in the candidate is "added").
func Compute(cand, pub *types.ManifestPayload) (*Result, error) {
	candIdx, err := indexByKey(cand)
	if err != nil {
		return nil, err
	}
	pubIdx, err := indexByKey(pub)
	if err != nil {
		return nil, err
	}

	rows := make([]Row, 0, len(candIdx)+len(pubIdx))
	keys := mergedKeys(candIdx, pubIdx)
	counts := map[string]int{
		DriftNone:          0,
		DriftAdded:         0,
		DriftRemoved:       0,
		DriftPriceChanged:  0,
		DriftModeChanged:   0,
		DriftExtraChanged:  0,
		DriftWorkerChanged: 0,
	}

	for _, k := range keys {
		c := candIdx[k]
		p := pubIdx[k]
		row := Row{Key: k, Candidate: c, Published: p}
		switch {
		case c != nil && p == nil:
			row.Drift = DriftAdded
			row.CapabilityID = c.CapabilityID
			row.OfferingID = c.OfferingID
			row.NewPriceWei = c.PricePerUnitWei
			row.NewMode = c.InteractionMode
			row.NewWorkerURL = c.WorkerURL
		case c == nil && p != nil:
			row.Drift = DriftRemoved
			row.CapabilityID = p.CapabilityID
			row.OfferingID = p.OfferingID
			row.OldPriceWei = p.PricePerUnitWei
			row.OldMode = p.InteractionMode
			row.OldWorkerURL = p.WorkerURL
		default:
			row.CapabilityID = c.CapabilityID
			row.OfferingID = c.OfferingID
			row.OldPriceWei = p.PricePerUnitWei
			row.NewPriceWei = c.PricePerUnitWei
			row.OldMode = p.InteractionMode
			row.NewMode = c.InteractionMode
			row.OldWorkerURL = p.WorkerURL
			row.NewWorkerURL = c.WorkerURL
			row.Drift = classify(c, p)
		}
		counts[row.Drift]++
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.CapabilityID != b.CapabilityID {
			return a.CapabilityID < b.CapabilityID
		}
		return a.OfferingID < b.OfferingID
	})

	return &Result{Rows: rows, Counts: counts}, nil
}

func classify(c, p *types.CapabilityTuple) string {
	if c.PricePerUnitWei != p.PricePerUnitWei {
		return DriftPriceChanged
	}
	if c.InteractionMode != p.InteractionMode {
		return DriftModeChanged
	}
	if c.WorkerURL != p.WorkerURL {
		return DriftWorkerChanged
	}
	if !mapsEqualCanonical(c.Extra, p.Extra) || !mapsEqualCanonical(c.Constraints, p.Constraints) {
		return DriftExtraChanged
	}
	return DriftNone
}

func indexByKey(m *types.ManifestPayload) (map[string]*types.CapabilityTuple, error) {
	out := make(map[string]*types.CapabilityTuple)
	if m == nil {
		return out, nil
	}
	for i := range m.Capabilities {
		c := m.Capabilities[i]
		key, err := uniquenessKey(&c)
		if err != nil {
			return nil, err
		}
		out[key] = &c
	}
	return out, nil
}

func uniquenessKey(c *types.CapabilityTuple) (string, error) {
	root := map[string]any{
		"capability_id": c.CapabilityID,
		"offering_id":   c.OfferingID,
	}
	if len(c.Extra) > 0 {
		root["extra"] = c.Extra
	}
	if len(c.Constraints) > 0 {
		root["constraints"] = c.Constraints
	}
	b, err := candidate.CanonicalBytes(root)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func mapsEqualCanonical(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	ab, err := candidate.CanonicalBytes(a)
	if err != nil {
		return false
	}
	bb, err := candidate.CanonicalBytes(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}

func mergedKeys(a, b map[string]*types.CapabilityTuple) []string {
	seen := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		seen[k] = struct{}{}
	}
	for k := range b {
		seen[k] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
