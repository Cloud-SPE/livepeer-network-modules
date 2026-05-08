// Package selection holds Filter + rank logic. Used by the resolver
// gRPC Select() RPC and (occasionally) by tests + the publisher's
// "did the manifest I just signed satisfy this filter?" sanity check.
//
// Selection is conjunctive (AND across criteria) and is stable
// within a single process: the input order of nodes determines tie
// breaks among equal-weight nodes.
package selection

import (
	"math"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// Filter parameters. Zero-value means "no constraint" for that field.
type Filter struct {
	Capability  string    // exact match against any node.capability.name
	Offering    string    // exact match against any offering.id within the capability
	Tier        string    // must be in node.TierAllowed (or TierAllowed nil → matches anything)
	MinWeight   int       // node.Weight >= MinWeight
	GeoCenter   *GeoPoint // optional center; combined with GeoWithinKM
	GeoWithinKM float64
}

// GeoPoint is a lat/lon pair.
type GeoPoint struct {
	Lat, Lon float64
}

// Apply returns the subset of nodes matching f, sorted by Weight desc.
// Stable sort: among equal-weight nodes, input order is preserved.
func Apply(nodes []types.ResolvedNode, f Filter) []types.ResolvedNode {
	out := make([]types.ResolvedNode, 0, len(nodes))
	for _, n := range nodes {
		if !matches(n, f) {
			continue
		}
		out = append(out, n)
	}
	stableSortByWeightDesc(out)
	return out
}

func matches(n types.ResolvedNode, f Filter) bool {
	if !n.Enabled {
		return false
	}
	if f.MinWeight > 0 && n.Weight < f.MinWeight {
		return false
	}
	if f.Tier != "" && len(n.TierAllowed) > 0 && !contains(n.TierAllowed, f.Tier) {
		return false
	}
	if f.Capability != "" {
		c, ok := findCap(n.Capabilities, f.Capability)
		if !ok {
			return false
		}
		if f.Offering != "" && !offeringPresent(c, f.Offering) {
			return false
		}
	} else if f.Offering != "" {
		// offering without capability: search across all capabilities.
		if !anyOffering(n.Capabilities, f.Offering) {
			return false
		}
	}
	if f.GeoCenter != nil && f.GeoWithinKM > 0 {
		if n.Lat == nil || n.Lon == nil {
			return false
		}
		if Haversine(*n.Lat, *n.Lon, f.GeoCenter.Lat, f.GeoCenter.Lon) > f.GeoWithinKM {
			return false
		}
	}
	return true
}

func findCap(caps []types.Capability, name string) (types.Capability, bool) {
	for _, c := range caps {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return types.Capability{}, false
}

func offeringPresent(c types.Capability, id string) bool {
	for _, o := range c.Offerings {
		if strings.EqualFold(o.ID, id) {
			return true
		}
	}
	return false
}

func anyOffering(caps []types.Capability, id string) bool {
	for _, c := range caps {
		if offeringPresent(c, id) {
			return true
		}
	}
	return false
}

func contains(s []string, v string) bool {
	for _, e := range s {
		if strings.EqualFold(e, v) {
			return true
		}
	}
	return false
}

// stableSortByWeightDesc sorts nodes in place by Weight descending,
// preserving input order among equal weights. Insertion sort — slices
// here are small (low hundreds at most).
func stableSortByWeightDesc(s []types.ResolvedNode) {
	for i := 1; i < len(s); i++ {
		j := i
		for j > 0 && s[j-1].Weight < s[j].Weight {
			s[j-1], s[j] = s[j], s[j-1]
			j--
		}
	}
}

// Haversine returns the great-circle distance between two points in
// kilometers. Used by geo-filtering.
func Haversine(lat1, lon1, lat2, lon2 float64) float64 {
	const r = 6371.0                                                   // Earth's mean radius in km
	rad := func(d float64) float64 { return d * 0.017453292519943295 } // π/180
	dlat := rad(lat2 - lat1)
	dlon := rad(lon2 - lon1)
	a := math.Sin(dlat/2)*math.Sin(dlat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * r * math.Asin(math.Sqrt(a))
}
