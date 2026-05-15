package roster

import (
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func TestBuildView_JoinsBrokerStatusToRow(t *testing.T) {
	now := time.Now().UTC()
	cand := &types.ManifestPayload{Capabilities: []types.CapabilityTuple{{
		CapabilityID: "cap", OfferingID: "off", InteractionMode: "m@v1",
		WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "100",
		WorkerURL: "https://w",
	}}}
	snap := scrape.Snapshot{
		WindowStart: now.Add(-30 * time.Second),
		WindowEnd:   now,
		Brokers: []scrape.BrokerStatus{
			{Name: "b1", BaseURL: "http://b1", Freshness: scrape.FreshnessOK, TupleHealth: map[string]types.BrokerHealthCapability{"cap|off": {ID: "cap", OfferingID: "off", Status: "ready", Reason: "probe_ok", StaleAfter: now.Add(time.Minute)}}},
			{Name: "b2", BaseURL: "http://b2", Freshness: scrape.FreshnessStaleFailing, LastError: "timeout", TupleHealth: map[string]types.BrokerHealthCapability{"cap|off": {ID: "cap", OfferingID: "off", Status: "degraded", Reason: "timeout", StaleAfter: now.Add(time.Minute)}}},
		},
		SourceTuples: []types.SourceTuple{
			{BrokerName: "b1", Offering: types.BrokerOffering{
				CapabilityID: "cap", OfferingID: "off", InteractionMode: "m@v1",
				WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "100",
			}},
			{BrokerName: "b2", Offering: types.BrokerOffering{
				CapabilityID: "cap", OfferingID: "off", InteractionMode: "m@v1",
				WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "100",
			}},
		},
	}
	v, err := BuildView("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cand, nil, snap)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(v.Rows))
	}
	if len(v.Rows[0].Brokers) != 2 {
		t.Fatalf("expected 2 broker cells, got %d", len(v.Rows[0].Brokers))
	}
	if got := v.Rows[0].Brokers[0].LiveStatus; got != "ready" {
		t.Fatalf("live status = %q, want ready", got)
	}
	if v.Rows[0].Drift != "added" {
		t.Fatalf("drift: %s", v.Rows[0].Drift)
	}
}

func TestBuildView_DriftCountsSurface(t *testing.T) {
	cand := &types.ManifestPayload{Capabilities: []types.CapabilityTuple{
		{CapabilityID: "a", OfferingID: "1", InteractionMode: "m@v1",
			WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "100",
			WorkerURL: "https://w"},
	}}
	pub := &types.ManifestPayload{Capabilities: []types.CapabilityTuple{
		{CapabilityID: "a", OfferingID: "1", InteractionMode: "m@v1",
			WorkUnit: types.WorkUnit{Name: "x"}, PricePerUnitWei: "200",
			WorkerURL: "https://w"},
	}}
	v, err := BuildView("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", cand, pub, scrape.Snapshot{})
	if err != nil {
		t.Fatal(err)
	}
	if v.DriftCounts["price_changed"] != 1 {
		t.Fatalf("counts: %v", v.DriftCounts)
	}
}

func TestApply_FilterBySubstring(t *testing.T) {
	v := &View{Rows: []Row{
		{CapabilityID: "openai:foo", OfferingID: "1"},
		{CapabilityID: "video:bar", OfferingID: "1"},
	}}
	got := v.Apply(Filter{CapabilitySubstring: "openai"})
	if len(got.Rows) != 1 || got.Rows[0].CapabilityID != "openai:foo" {
		t.Fatalf("got %+v", got.Rows)
	}
}

func TestApply_FilterByDrift(t *testing.T) {
	v := &View{Rows: []Row{
		{CapabilityID: "a", Drift: "none"},
		{CapabilityID: "b", Drift: "price_changed"},
	}}
	got := v.Apply(Filter{DriftKind: "price_changed"})
	if len(got.Rows) != 1 || got.Rows[0].CapabilityID != "b" {
		t.Fatalf("got %+v", got.Rows)
	}
}
