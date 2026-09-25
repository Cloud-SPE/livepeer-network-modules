package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWireCatalogIdentityMetadataEligibilityAndCoverage(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "worker", URL: "https://broker.example.com", Capabilities: []types.Capability{{Name: "example:work", WorkUnit: "unit", Protocol: "paid-job/v1", Offerings: []types.Offering{{ID: "standard", PricePerWorkUnitWei: "7", PerUnits: 60, Constraints: []byte(`{"limit":2}`)}}}}}})
	var envelope types.CoordinatorSignedManifest
	if err := json.Unmarshal(f.fetcher.Bodies[f.uri], &envelope); err != nil {
		t.Fatal(err)
	}
	c := &envelope.Manifest.Capabilities[0]
	c.PerUnits = 60
	c.WorkUnit.Estimator = &types.CoordinatorEstimator{ID: "example-estimator/v1", Rounding: "ceil", Exactness: "upper-bound"}
	c.Job = json.RawMessage(`{"transports":["multipart"],"x-new-axis":"opaque"}`)
	c.Constraints = map[string]any{"limit": float64(2)}
	canonical, err := types.CoordinatorCanonicalBytes(envelope.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := f.signerKey.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	envelope.Signature.Value = "0x" + hexLower(sig)
	body, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = body

	client := registryv1.NewResolverClient(f.clientConn)
	if _, err := client.ResolveByAddress(context.Background(), &registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)}); err != nil {
		t.Fatal(err)
	}
	other := types.EthAddress("0x1111111111111111111111111111111111111111")
	f.server.resolverSvc.Discover([]types.EthAddress{f.addr, other})
	result, err := client.ListOfferings(context.Background(), &registryv1.ListOfferingsRequest{})
	if err != nil || len(result.GetEntries()) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	e := result.Entries[0]
	r := e.Offering
	if r.EthAddress != string(f.addr) || r.WorkerUrl != "https://broker.example.com" || e.WorkerId == "" || r.Protocol != "paid-job/v1" || r.WorkUnit != "unit" || !e.Selectable {
		t.Fatal(e)
	}
	if r.UnitsPerPrice != 60 || r.WorkUnitEstimator.GetId() != "example-estimator/v1" || !bytes.Contains(r.ExtraJson, []byte("x-new-axis")) || !bytes.Contains(r.ConstraintsJson, []byte("limit")) {
		t.Fatal("offering metadata lost", r)
	}
	selected, err := client.SelectMany(context.Background(), &registryv1.SelectRequest{Capability: r.Capability, Offering: r.Offering})
	if err != nil || len(selected.Routes) != 1 || selected.Routes[0].RouteFingerprint == nil {
		t.Fatal(selected, err)
	}
	if result.Completeness != registryv1.CatalogCompleteness_CATALOG_COMPLETENESS_PARTIAL || result.Coverage.KnownAddresses != 2 {
		t.Fatal(result)
	}
	filtered, err := client.ListOfferings(context.Background(), &registryv1.ListOfferingsRequest{Capability: "absent"})
	if err != nil || len(filtered.Entries) != 0 || filtered.Coverage.KnownAddresses != 2 {
		t.Fatal(filtered, err)
	}
	known, err := client.ListKnown(context.Background(), &registryv1.ListKnownRequest{})
	if err != nil || len(known.Entries) != 2 {
		t.Fatal(known, err)
	}
	f.clk.Advance(11 * time.Minute)
	expired, err := client.ListOfferings(context.Background(), &registryv1.ListOfferingsRequest{IncludeExpired: true})
	if err != nil || len(expired.Entries) != 1 || expired.Entries[0].Selectable || !expired.Entries[0].Expired {
		t.Fatal(expired, err)
	}
}
func TestWireDeferredCarriesTypedDetailsAndRetryInfo(t *testing.T) {
	f := newWireFixture(t)
	client := registryv1.NewResolverClient(f.clientConn)
	req := &registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)}
	if _, err := client.ResolveByAddress(context.Background(), req); err == nil {
		t.Fatal("missing fixture unexpectedly resolved")
	}
	_, err := client.ResolveByAddress(context.Background(), req)
	if status.Code(err) != codes.Unavailable || extractCode(err) != "resolution_deferred" {
		t.Fatal(err)
	}
	var detail *registryv1.RegistryResolutionDetail
	var retry *errdetails.RetryInfo
	for _, d := range status.Convert(err).Details() {
		switch d := d.(type) {
		case *registryv1.RegistryResolutionDetail:
			detail = d
		case *errdetails.RetryInfo:
			retry = d
		}
	}
	if detail == nil || retry == nil || detail.EthAddress != string(f.addr) || detail.DiscoveryStatus.ConsecutiveFailures != 1 || detail.DiscoveryStatus.Compatibility != registryv1.ManifestCompatibility_MANIFEST_COMPATIBILITY_UNKNOWN || retry.RetryDelay.AsDuration() <= 0 {
		t.Fatal(detail, retry)
	}
}
