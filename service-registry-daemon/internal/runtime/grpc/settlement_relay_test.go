package grpc

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// Exercise the actual proto serialization, not only the internal route converter.
func TestWire_SelectManyRelaysManifestSettlementKeys(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{
		{URL: "https://broker-a.example.com", Capabilities: []types.Capability{{Name: "example:work", WorkUnit: "unit", Offerings: []types.Offering{{ID: "default", PricePerWorkUnitWei: "10"}}}}},
		{URL: "https://broker-b.example.com", Capabilities: []types.Capability{{Name: "example:work", WorkUnit: "unit", Offerings: []types.Offering{{ID: "default", PricePerWorkUnitWei: "20"}}}}},
	})
	var env types.CoordinatorSignedManifest
	if err := json.Unmarshal(f.fetcher.Bodies[f.uri], &env); err != nil {
		t.Fatal(err)
	}
	env.Manifest.PublicationSeq = 10
	for _, digit := range []string{"11", "22"} {
		env.Manifest.SettlementKeys = append(env.Manifest.SettlementKeys, types.CoordinatorSettlementKey{PublicKey: "0x04" + strings.Repeat(digit, 64), NotBefore: f.clk.Now().Add(-time.Hour), ExpiresAt: f.clk.Now().Add(time.Hour)})
	}
	canonical, err := types.CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := f.signerKey.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature.Value = "0x" + hex.EncodeToString(sig)
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = raw
	overlay, err := config.ParseOverlayYAML([]byte(fmt.Sprintf("overlay:\n  - eth_address: %q\n    manifest_url: %q\n", f.addr, f.uri)))
	if err != nil {
		t.Fatal(err)
	}
	// No chain provider: the configured coordinator is the only discovery source.
	f.server.resolverSvc = resolver.New(resolver.Config{Fetcher: f.fetcher, Cache: f.cache, Audit: f.auditRepo, Clock: f.clk, Overlay: func() *config.Overlay { return overlay }, OverlayOnly: true, RejectUnsigned: true})
	client := registryv1.NewResolverClient(f.clientConn)
	for _, phase := range []string{"cold", "cached", "refreshed"} {
		t.Run(phase, func(t *testing.T) {
			if phase == "refreshed" {
				if _, err := client.Refresh(context.Background(), &registryv1.RefreshRequest{EthAddress: string(f.addr), Force: true}); err != nil {
					t.Fatal(err)
				}
			}
			result, err := client.SelectMany(context.Background(), &registryv1.SelectRequest{Capability: "example:work", Offering: "default"})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.GetRoutes()) != 2 {
				t.Fatalf("routes=%d", len(result.GetRoutes()))
			}
			for _, route := range result.GetRoutes() {
				if len(route.GetSettlementKeys()) != 2 {
					t.Fatalf("%s dropped keys: %+v", route.GetWorkerUrl(), route)
				}
				got := map[string]*registryv1.SettlementKey{}
				for _, k := range route.GetSettlementKeys() {
					got[k.GetPublicKey()] = k
				}
				for _, want := range env.Manifest.SettlementKeys {
					k := got[want.PublicKey]
					if k == nil || k.GetNotBefore() != want.NotBefore.UTC().Format(time.RFC3339) || k.GetExpiresAt() != want.ExpiresAt.UTC().Format(time.RFC3339) || k.GetIntroducedInPublicationSeq() != 10 {
						t.Fatalf("delegation changed: %+v", k)
					}
				}
			}
		})
	}
}
