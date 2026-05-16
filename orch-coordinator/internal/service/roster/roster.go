// Package roster materializes the operator-facing roster view from
// the candidate, the currently-published manifest, the diff result,
// and the per-broker freshness state. Rows are capability tuples; the
// row identity uses the §5 / Q2 uniqueness key.
//
// The roster is the read-only operator UX from plan 0018 §8. Filtering
// + search are computed here; the web UI in commit 6 hands query
// params to BuildView.
package roster

import (
	"sort"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/diff"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

// Row is one roster entry. Fields are 1:1 with the columns in plan
// 0018 §8: capability/offering, mode, price, brokers, published?,
// drift.
type Row struct {
	CapabilityID    string
	OfferingID      string
	InteractionMode string
	PriceWei        string
	Brokers         []BrokerCell
	WorkerURL       string
	Published       bool
	PublishedTuple  *types.CapabilityTuple
	Drift           string
	OldPriceWei     string
	NewPriceWei     string
}

// BrokerCell describes one broker that advertises this row's tuple,
// with its current freshness and any error.
type BrokerCell struct {
	Name               string
	Freshness          string
	Error              string
	LiveStatus         string
	LiveReason         string
	HealthStale        bool
	MetadataState      string
	MetadataResult     string
	MetadataError      string
	MetadataAgeSeconds float64
	MetadataFailures   int
}

// View is the rendered roster.
type View struct {
	OrchEthAddress string
	Rows           []Row
	BrokerStatus   []scrape.BrokerStatus
	DriftCounts    map[string]int
}

// Filter narrows the roster output. Empty fields mean "no filter".
type Filter struct {
	CapabilitySubstring string
	Mode                string
	BrokerName          string
	DriftKind           string
}

// BuildView assembles a roster from the inputs. Either of cand / pub
// may be nil; the broker status comes from the latest scrape
// snapshot.
func BuildView(orchEthAddress string, cand, pub *types.ManifestPayload, snap scrape.Snapshot) (*View, error) {
	d, err := diff.Compute(cand, pub)
	if err != nil {
		return nil, err
	}
	brokersByKey := brokersByUniquenessKey(snap)

	rows := make([]Row, 0, len(d.Rows))
	for _, dr := range d.Rows {
		row := Row{
			CapabilityID: dr.CapabilityID,
			OfferingID:   dr.OfferingID,
			Drift:        dr.Drift,
			OldPriceWei:  dr.OldPriceWei,
			NewPriceWei:  dr.NewPriceWei,
		}
		if dr.Candidate != nil {
			row.InteractionMode = dr.Candidate.InteractionMode
			row.PriceWei = dr.Candidate.PricePerUnitWei
			row.WorkerURL = dr.Candidate.WorkerURL
		}
		if dr.Published != nil {
			row.Published = true
			row.PublishedTuple = dr.Published
			if dr.Candidate == nil {
				row.InteractionMode = dr.Published.InteractionMode
				row.PriceWei = dr.Published.PricePerUnitWei
				row.WorkerURL = dr.Published.WorkerURL
			}
		}
		if cells, ok := brokersByKey[dr.Key]; ok {
			row.Brokers = cells
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if a.CapabilityID != b.CapabilityID {
			return a.CapabilityID < b.CapabilityID
		}
		return a.OfferingID < b.OfferingID
	})

	return &View{
		OrchEthAddress: orchEthAddress,
		Rows:           rows,
		BrokerStatus:   snap.Brokers,
		DriftCounts:    d.Counts,
	}, nil
}

// Apply returns a copy of v narrowed by the filter.
func (v *View) Apply(f Filter) *View {
	out := &View{
		OrchEthAddress: v.OrchEthAddress,
		BrokerStatus:   v.BrokerStatus,
		DriftCounts:    map[string]int{},
	}
	for _, r := range v.Rows {
		if f.CapabilitySubstring != "" && !strings.Contains(r.CapabilityID, f.CapabilitySubstring) {
			continue
		}
		if f.Mode != "" && r.InteractionMode != f.Mode {
			continue
		}
		if f.DriftKind != "" && r.Drift != f.DriftKind {
			continue
		}
		if f.BrokerName != "" {
			ok := false
			for _, b := range r.Brokers {
				if b.Name == f.BrokerName {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		out.Rows = append(out.Rows, r)
		out.DriftCounts[r.Drift]++
	}
	return out
}

// brokersByUniquenessKey indexes the snapshot's source tuples by the
// canonical uniqueness key. Same key form the diff service uses; the
// roster joins on that.
func brokersByUniquenessKey(snap scrape.Snapshot) map[string][]BrokerCell {
	out := make(map[string][]BrokerCell)
	freshness := make(map[string]scrape.BrokerStatus, len(snap.Brokers))
	for _, b := range snap.Brokers {
		freshness[b.Name] = b
	}
	seenPerKey := make(map[string]map[string]struct{})
	for _, st := range snap.SourceTuples {
		k := uniquenessKey(st.Offering.CapabilityID, st.Offering.OfferingID, st.Offering.Extra, st.Offering.Constraints)
		if _, ok := seenPerKey[k]; !ok {
			seenPerKey[k] = map[string]struct{}{}
		}
		if _, dup := seenPerKey[k][st.BrokerName]; dup {
			continue
		}
		seenPerKey[k][st.BrokerName] = struct{}{}
		cell := BrokerCell{Name: st.BrokerName}
		if b, ok := freshness[st.BrokerName]; ok {
			cell.Freshness = b.Freshness
			cell.Error = b.LastError
			if h, ok := b.TupleHealth[tupleHealthKey(st.Offering.CapabilityID, st.Offering.OfferingID)]; ok {
				cell.LiveStatus = h.Status
				cell.LiveReason = h.Reason
				if !h.StaleAfter.IsZero() && h.StaleAfter.Before(stampNow()) {
					cell.HealthStale = true
				}
				if h.Metadata != nil {
					cell.MetadataState, cell.MetadataAgeSeconds = types.ClassifyBrokerHealthMetadata(
						h.Metadata,
						snap.MetadataWarningAfter,
						snap.MetadataStaleAfter,
					)
					cell.MetadataResult = h.Metadata.LastResult
					cell.MetadataError = h.Metadata.LastError
					cell.MetadataFailures = h.Metadata.ConsecutiveFailures
				}
			}
		}
		out[k] = append(out[k], cell)
	}
	for k := range out {
		sort.Slice(out[k], func(i, j int) bool { return out[k][i].Name < out[k][j].Name })
	}
	return out
}

var stampNow = func() time.Time { return time.Now().UTC() }

func tupleHealthKey(capabilityID, offeringID string) string {
	return capabilityID + "|" + offeringID
}

func uniquenessKey(capID, offeringID string, extra, constraints map[string]any) string {
	root := map[string]any{
		"capability_id": capID,
		"offering_id":   offeringID,
	}
	if len(extra) > 0 {
		root["extra"] = extra
	}
	if len(constraints) > 0 {
		root["constraints"] = constraints
	}
	b, err := candidate.CanonicalBytes(root)
	if err != nil {
		return capID + "|" + offeringID
	}
	return string(b)
}
