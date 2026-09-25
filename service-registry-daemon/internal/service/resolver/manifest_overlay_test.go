package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func useManifestOverlay(t *testing.T, f *fixture, uri string) {
	t.Helper()
	o, err := config.ParseOverlayYAML([]byte(fmt.Sprintf("overlay:\n  - eth_address: %q\n    manifest_url: %q\n", f.addr, uri)))
	if err != nil {
		t.Fatal(err)
	}
	f.svc.overlay = func() *config.Overlay { return o }
	f.svc.rejectUns = true
	// Any accidental chain lookup panics; overlay discovery must be independent.
	f.svc.chain = nil
}

func publishOverlayFixture(t *testing.T, f *fixture, price string) {
	t.Helper()
	body := f.signManifestForFixture([]types.Node{{ID: "broker", URL: "https://broker.example.com", Capabilities: []types.Capability{{Name: "example:work", Protocol: "paid-job/v1", WorkUnit: "unit", Offerings: []types.Offering{{ID: "default", PricePerWorkUnitWei: price}}}}}})
	var env types.CoordinatorSignedManifest
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	if cached, ok, err := f.cache.Get(f.addr); err == nil && ok {
		env.Manifest.PublicationSeq = cached.PublicationSeq + 1
	}
	env.Manifest.SettlementKeys = []types.CoordinatorSettlementKey{{PublicKey: "0x04" + strings.Repeat("11", 64), NotBefore: f.clk.Now().Add(-time.Hour), ExpiresAt: f.clk.Now().Add(time.Hour)}}
	canonical, err := types.CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := f.signer.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature.Value = "0x" + hex(sig)
	body, err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = body
}

func TestManifestOverlay_ParityAndRefresh(t *testing.T) {
	f := newFixture(t)
	publishOverlayFixture(t, f, "10")
	ctx := context.Background()
	req := Request{Address: f.addr}
	chainResult, err := f.svc.ResolveByAddress(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(chainResult.Nodes) != 1 || len(chainResult.Nodes[0].SettlementKeys) != 1 {
		t.Fatalf("missing routes or settlement keys: %+v", chainResult)
	}
	useManifestOverlay(t, f, f.uri)
	overlayResult, err := f.svc.ResolveByAddress(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(chainResult.Nodes, overlayResult.Nodes) {
		t.Fatalf("chain/overlay routes differ:\n%+v\n%+v", chainResult.Nodes, overlayResult.Nodes)
	}
	if overlayResult.Nodes[0].SignatureStatus != types.SigVerified {
		t.Fatal("overlay route must be verified")
	}
	publishOverlayFixture(t, f, "20")
	assertPrice := func(want string) {
		t.Helper()
		got, err := f.svc.ResolveByAddress(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		if price := got.Nodes[0].Capabilities[0].Offerings[0].PricePerWorkUnitWei; price != want {
			t.Fatalf("price %s, want %s", price, want)
		}
	}
	assertPrice("10")
	f.clk.T = f.clk.T.Add(11 * time.Minute)
	assertPrice("20")
	publishOverlayFixture(t, f, "30")
	req.ForceRefresh = true
	assertPrice("30")
	// Changing the configured pointer cannot reuse a fresh entry for the old URL.
	useManifestOverlay(t, f, "https://other.example/.well-known/livepeer-registry.json")
	req.ForceRefresh = false
	req.AllowLegacyFallback = true
	if _, err := f.svc.ResolveByAddress(ctx, req); !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("changed URL reused cache or downgraded: %v", err)
	}
}

func TestManifestOverlay_OutageAndInvalidPublication(t *testing.T) {
	for _, failure := range []string{"outage", "wrong-address", "unsigned", "tampered", "invalid-json"} {
		t.Run(failure, func(t *testing.T) {
			f := newFixture(t)
			// A non-well-known path also exercises alternate-probe error precedence.
			f.uri = "https://orch.example.com/manifest.json"
			publishOverlayFixture(t, f, "10")
			useManifestOverlay(t, f, f.uri)
			req := Request{Address: f.addr, AllowLegacyFallback: true, AllowUnsigned: true}
			if _, err := f.svc.ResolveByAddress(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			f.clk.T = f.clk.T.Add(11 * time.Minute)
			body := f.fetcher.Bodies[f.uri]
			switch failure {
			case "outage":
				delete(f.fetcher.Bodies, f.uri)
			case "invalid-json":
				f.fetcher.Bodies[f.uri] = []byte("not json")
			default:
				var env types.CoordinatorSignedManifest
				if err := json.Unmarshal(body, &env); err != nil {
					t.Fatal(err)
				}
				switch failure {
				case "wrong-address":
					env.Manifest.Orch.EthAddress = "0x" + strings.Repeat("ab", 20)
				case "unsigned":
					env.Signature.Value = ""
				case "tampered":
					env.Manifest.Capabilities[0].PricePerUnitWei = "999"
				}
				changed, err := json.Marshal(env)
				if err != nil {
					t.Fatal(err)
				}
				f.fetcher.Bodies[f.uri] = changed
			}
			got, err := f.svc.ResolveByAddress(context.Background(), req)
			if failure != "outage" {
				if err == nil {
					t.Fatalf("invalid publication returned routes: %+v", got)
				}
				return
			}
			if err != nil || got.FreshnessStatus != types.StaleFailing {
				t.Fatalf("expected bounded last-good: %+v %v", got, err)
			}
			f.clk.T = f.clk.T.Add(time.Hour)
			if _, err := f.svc.ResolveByAddress(context.Background(), req); !errors.Is(err, types.ErrManifestUnavailable) {
				t.Fatalf("expired cache used or unsigned fallback: %v", err)
			}
		})
	}
}

func TestManifestOverlay_CandidatesRetryFailedStartup(t *testing.T) {
	f := newFixture(t)
	useManifestOverlay(t, f, f.uri)
	if _, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr}); err == nil {
		t.Fatal("expected initial fetch failure")
	}
	addrs, err := f.svc.CandidateAddresses()
	if err != nil || len(addrs) != 1 || addrs[0] != f.addr {
		t.Fatalf("lost configured discovery after failure: %v %v", addrs, err)
	}
	publishOverlayFixture(t, f, "10")
	if _, err := f.svc.ResolveByAddress(context.Background(), Request{Address: addrs[0], ForceRefresh: true}); err != nil {
		t.Fatal(err)
	}
	addrs, err = f.svc.CandidateAddresses()
	if err != nil || len(addrs) != 1 {
		t.Fatalf("duplicate cached/configured address: %v %v", addrs, err)
	}
}

func TestManifestOverlay_OverlayOnlyExcludesOldChainCache(t *testing.T) {
	f := newFixture(t)
	publishOverlayFixture(t, f, "10")
	req := Request{Address: f.addr}
	if _, err := f.svc.ResolveByAddress(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	f.svc.overlayOnly = true
	f.svc.chain = nil
	addrs, err := f.svc.CandidateAddresses()
	if err != nil || len(addrs) != 0 {
		t.Fatalf("old chain cache escaped configured list: %v %v", addrs, err)
	}
	if _, err := f.svc.ResolveByAddress(context.Background(), req); !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("unlisted address resolved: %v", err)
	}
	// Reconfigure this address as a static pin: an old signed chain cache
	// must not supply the previous capabilities or settlement delegation.
	o, err := config.ParseOverlayYAML([]byte(fmt.Sprintf("overlay:\n  - eth_address: %q\n    unsigned_allowed: true\n    pin:\n      - id: static\n        url: https://static.example.com\n", f.addr)))
	if err != nil {
		t.Fatal(err)
	}
	f.svc.overlay = func() *config.Overlay { return o }
	res, err := f.svc.ResolveByAddress(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != types.ModeStaticOverlay || len(res.Nodes) != 1 || res.Nodes[0].ID != "static" || len(res.Nodes[0].SettlementKeys) != 0 {
		t.Fatalf("reused old chain publication: %+v", res)
	}
}
