package selection

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

func mkNode(id string, weight int, caps []string, models []string, tier []string, lat, lon *float64) types.ResolvedNode {
	cs := make([]types.Capability, len(caps))
	for i, c := range caps {
		cap := types.Capability{Name: c}
		if i < len(models) && models[i] != "" {
			cap.Offerings = []types.Offering{{ID: models[i]}}
		}
		cs[i] = cap
	}
	return types.ResolvedNode{
		ID:           id,
		Weight:       weight,
		Enabled:      true,
		Capabilities: cs,
		TierAllowed:  tier,
		Lat:          lat,
		Lon:          lon,
	}
}

func TestApply_FilterByCapability(t *testing.T) {
	nodes := []types.ResolvedNode{
		mkNode("a", 50, []string{"openai:/v1/chat/completions"}, []string{"gpt-1"}, nil, nil, nil),
		mkNode("b", 100, []string{"openai:/v1/embeddings"}, []string{"emb-1"}, nil, nil, nil),
	}
	got := Apply(nodes, Filter{Capability: "openai:/v1/embeddings"})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only b, got %+v", got)
	}
}

func TestApply_FilterByOffering(t *testing.T) {
	nodes := []types.ResolvedNode{
		mkNode("a", 50, []string{"x"}, []string{"m1"}, nil, nil, nil),
		mkNode("b", 50, []string{"x"}, []string{"m2"}, nil, nil, nil),
	}
	got := Apply(nodes, Filter{Capability: "x", Offering: "m2"})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected only b, got %+v", got)
	}
}

func TestApply_TierFilterAllowsUnscoped(t *testing.T) {
	nodes := []types.ResolvedNode{
		mkNode("a", 50, []string{"x"}, nil, []string{"prepaid"}, nil, nil),
		mkNode("b", 50, []string{"x"}, nil, nil, nil, nil), // no tier => matches anything
	}
	got := Apply(nodes, Filter{Tier: "free"})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected b only, got %+v", got)
	}
}

func TestApply_SortsByWeightDescStable(t *testing.T) {
	nodes := []types.ResolvedNode{
		mkNode("a", 10, []string{"x"}, nil, nil, nil, nil),
		mkNode("b", 30, []string{"x"}, nil, nil, nil, nil),
		mkNode("c", 30, []string{"x"}, nil, nil, nil, nil),
		mkNode("d", 20, []string{"x"}, nil, nil, nil, nil),
	}
	got := Apply(nodes, Filter{})
	want := []string{"b", "c", "d", "a"}
	for i, n := range got {
		if n.ID != want[i] {
			t.Fatalf("at %d: got %s, want %s (full %+v)", i, n.ID, want[i], got)
		}
	}
}

func TestApply_DropsDisabled(t *testing.T) {
	nodes := []types.ResolvedNode{
		mkNode("a", 100, []string{"x"}, nil, nil, nil, nil),
		mkNode("b", 100, []string{"x"}, nil, nil, nil, nil),
	}
	nodes[0].Enabled = false
	got := Apply(nodes, Filter{})
	if len(got) != 1 || got[0].ID != "b" {
		t.Fatalf("expected b only, got %+v", got)
	}
}

func TestApply_GeoFilter(t *testing.T) {
	nyLat, nyLon := 40.71, -74.00
	laLat, laLon := 34.05, -118.25
	nodes := []types.ResolvedNode{
		mkNode("ny", 100, nil, nil, nil, &nyLat, &nyLon),
		mkNode("la", 100, nil, nil, nil, &laLat, &laLon),
	}
	// Within 100km of NY: only NY
	got := Apply(nodes, Filter{GeoCenter: &GeoPoint{Lat: 40.7, Lon: -74.0}, GeoWithinKM: 100})
	if len(got) != 1 || got[0].ID != "ny" {
		t.Fatalf("expected ny only, got %+v", got)
	}
}

func TestHaversine_KnownDistance(t *testing.T) {
	// NY ↔ LA ≈ 3940 km, allow 50 km tolerance.
	d := Haversine(40.71, -74.00, 34.05, -118.25)
	if d < 3900 || d > 3990 {
		t.Fatalf("haversine NY-LA = %v km", d)
	}
}
