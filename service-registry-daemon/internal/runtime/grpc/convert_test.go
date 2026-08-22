package grpc

import (
	"testing"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/selection"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func TestResolveModeToProto(t *testing.T) {
	cases := []struct {
		in   types.ResolveMode
		want registryv1.ResolveMode
	}{
		{types.ModeWellKnown, registryv1.ResolveMode_RESOLVE_MODE_WELL_KNOWN},
		{types.ModeCSV, registryv1.ResolveMode_RESOLVE_MODE_CSV},
		{types.ModeLegacy, registryv1.ResolveMode_RESOLVE_MODE_LEGACY},
		{types.ModeUnknown, registryv1.ResolveMode_RESOLVE_MODE_UNSPECIFIED},
		{types.ResolveMode(99), registryv1.ResolveMode_RESOLVE_MODE_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := resolveModeToProto(c.in); got != c.want {
			t.Fatalf("resolveModeToProto(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSignatureStatusToProto(t *testing.T) {
	cases := []struct {
		in   types.SignatureStatus
		want registryv1.SignatureStatus
	}{
		{types.SigVerified, registryv1.SignatureStatus_SIGNATURE_STATUS_VERIFIED},
		{types.SigUnsigned, registryv1.SignatureStatus_SIGNATURE_STATUS_UNSIGNED},
		{types.SigLegacy, registryv1.SignatureStatus_SIGNATURE_STATUS_LEGACY},
		{types.SigUnknown, registryv1.SignatureStatus_SIGNATURE_STATUS_UNSPECIFIED},
		{types.SigInvalid, registryv1.SignatureStatus_SIGNATURE_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := signatureStatusToProto(c.in); got != c.want {
			t.Fatalf("signatureStatusToProto(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestFreshnessToProto(t *testing.T) {
	cases := []struct {
		in   types.FreshnessStatus
		want registryv1.FreshnessStatus
	}{
		{types.Fresh, registryv1.FreshnessStatus_FRESHNESS_STATUS_FRESH},
		{types.StaleRecoverable, registryv1.FreshnessStatus_FRESHNESS_STATUS_STALE_RECOVERABLE},
		{types.StaleFailing, registryv1.FreshnessStatus_FRESHNESS_STATUS_STALE_FAILING},
		{types.FreshUnknown, registryv1.FreshnessStatus_FRESHNESS_STATUS_UNSPECIFIED},
	}
	for _, c := range cases {
		if got := freshnessToProto(c.in); got != c.want {
			t.Fatalf("freshnessToProto(%v) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSourceRoundTrip(t *testing.T) {
	cases := []types.Source{
		types.SourceUnknown,
		types.SourceManifest,
		types.SourceLegacy,
		types.SourceStaticOverlay,
		types.SourceCSVFallback,
	}
	for _, c := range cases {
		if got := sourceFromProto(sourceToProto(c)); got != c {
			t.Fatalf("source round-trip: %v -> %v", c, got)
		}
	}
}

func TestSignatureStatusFromProto_AllCases(t *testing.T) {
	cases := []registryv1.SignatureStatus{
		registryv1.SignatureStatus_SIGNATURE_STATUS_VERIFIED,
		registryv1.SignatureStatus_SIGNATURE_STATUS_UNSIGNED,
		registryv1.SignatureStatus_SIGNATURE_STATUS_LEGACY,
		registryv1.SignatureStatus_SIGNATURE_STATUS_UNSPECIFIED,
	}
	want := []types.SignatureStatus{
		types.SigVerified,
		types.SigUnsigned,
		types.SigLegacy,
		types.SigUnknown,
	}
	for i, c := range cases {
		if got := signatureStatusFromProto(c); got != want[i] {
			t.Fatalf("signatureStatusFromProto(%v) = %v, want %v", c, got, want[i])
		}
	}
}

func TestCapabilityRoundTrip(t *testing.T) {
	in := types.Capability{
		Name:     "openai:/v1/chat/completions",
		WorkUnit: "token",
		Offerings: []types.Offering{
			{ID: "gpt-1", PricePerWorkUnitWei: "1000", Constraints: []byte(`{"k":1}`)},
		},
		Extra: []byte(`{"a":1}`),
	}
	round := capabilityFromProto(capabilityToProto(in))
	if round.Name != in.Name || round.WorkUnit != in.WorkUnit {
		t.Fatalf("name/work_unit drift: %+v", round)
	}
	if len(round.Offerings) != 1 {
		t.Fatalf("models drift: %+v", round.Offerings)
	}
	if round.Offerings[0].ID != "gpt-1" || round.Offerings[0].PricePerWorkUnitWei != "1000" {
		t.Fatalf("model fields drift: %+v", round.Offerings[0])
	}
	if string(round.Extra) != `{"a":1}` {
		t.Fatalf("extra drift: %s", round.Extra)
	}
	if string(round.Offerings[0].Constraints) != `{"k":1}` {
		t.Fatalf("constraints drift: %s", round.Offerings[0].Constraints)
	}
}

func TestCapabilityFromProto_NilSafe(t *testing.T) {
	got := capabilityFromProto(nil)
	if got.Name != "" || got.Offerings != nil {
		t.Fatalf("nil-safe: %+v", got)
	}
}

func TestModelFromProto_NilSafe(t *testing.T) {
	got := offeringFromProto(nil)
	if got.ID != "" {
		t.Fatalf("nil-safe: %+v", got)
	}
}

func TestResolvedNodeRoundTrip(t *testing.T) {
	in := types.ResolvedNode{
		ID:               "n1",
		URL:              "https://x.test",
		WorkerEthAddress: "0x1111111111111111111111111111111111111111",
		Extra:            []byte(`{"region":"us-east-1"}`),
		Capabilities:     []types.Capability{{Name: "x"}},
		Source:           types.SourceManifest,
		SignatureStatus:  types.SigVerified,
		OperatorAddr:     types.EthAddress("0xabcdef0000000000000000000000000000000000"),
		Enabled:          true,
		TierAllowed:      []string{"free"},
		Weight:           50,
	}
	out := resolvedNodeFromProto(resolvedNodeToProto(in))
	if out.ID != in.ID || out.URL != in.URL || out.WorkerEthAddress != in.WorkerEthAddress {
		t.Fatalf("scalar drift: %+v", out)
	}
	if string(out.Extra) != string(in.Extra) {
		t.Fatalf("extra drift: %s", out.Extra)
	}
	if out.Source != types.SourceManifest || out.SignatureStatus != types.SigVerified {
		t.Fatalf("enum drift: %+v %+v", out.Source, out.SignatureStatus)
	}
	if !out.Enabled || out.Weight != 50 {
		t.Fatalf("policy drift: %+v", out)
	}
	if len(out.TierAllowed) != 1 || out.TierAllowed[0] != "free" {
		t.Fatalf("tier drift: %+v", out.TierAllowed)
	}
}

func TestResolvedNodeFromProto_NilSafe(t *testing.T) {
	if got := resolvedNodeFromProto(nil); got.ID != "" {
		t.Fatalf("nil-safe: %+v", got)
	}
}

func TestSelectedRouteToProto_NilSafe(t *testing.T) {
	if got := selectedRouteToProto(nil); got == nil {
		t.Fatal("nil-safe expected non-nil empty proto")
	}
}

func TestSelectedRouteFromResolvedNode(t *testing.T) {
	in := types.ResolvedNode{
		URL:            "https://worker.example.com",
		OperatorAddr:   "0xabcdef0000000000000000000000000000000000",
		PublicationSeq: 42,
		Capabilities: []types.Capability{
			{
				Name:     "openai:/v1/chat/completions",
				WorkUnit: "token",
				Extra:    []byte(`{"family":"chat"}`),
				Offerings: []types.Offering{
					{ID: "gpt-oss-20b", PricePerWorkUnitWei: "1000", Constraints: []byte(`{"max_context":8192}`)},
				},
			},
		},
	}
	out, err := selectedRouteFromResolvedNode(in, selection.Filter{
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-oss-20b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.WorkerURL != in.URL || out.EthAddress != string(in.OperatorAddr) {
		t.Fatalf("route drift: %+v", out)
	}
	if out.WorkUnit != "token" || out.PricePerWorkUnitWei != "1000" {
		t.Fatalf("pricing drift: %+v", out)
	}
	if string(out.Extra) != `{"family":"chat"}` || string(out.Constraints) != `{"max_context":8192}` {
		t.Fatalf("opaque field drift: %+v", out)
	}
	if out.UnitsPerPrice != 1 || out.QuoteVersion != 42 {
		t.Fatalf("quote fields drift: %+v", out)
	}
	if out.QuoteID == "" || len(out.ConstraintFingerprint) == 0 || len(out.RouteFingerprint) == 0 {
		t.Fatalf("fingerprints missing: %+v", out)
	}
}

func TestSelectedRouteFromResolvedNode_MissingMatch(t *testing.T) {
	_, err := selectedRouteFromResolvedNode(types.ResolvedNode{
		Capabilities: []types.Capability{{Name: "x", Offerings: []types.Offering{{ID: "a"}}}},
	}, selection.Filter{Capability: "x", Offering: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestSelectedRouteFromResolvedNode_EmptyConstraints locks in the
// resolver-side guarantee: even when an offering publishes no
// constraints block, the selected route still carries a non-nil
// deterministic ConstraintFingerprint (the canonical-JSON `{}` digest).
// Without this, downstream payment-daemon and request-path filters that
// require a non-empty fingerprint would reject otherwise-valid pool
// routes.
func TestSelectedRouteFromResolvedNode_EmptyConstraints(t *testing.T) {
	build := func(constraints []byte) *SelectedRoute {
		in := types.ResolvedNode{
			URL:          "https://worker.example.com",
			OperatorAddr: "0xabcdef0000000000000000000000000000000000",
			Capabilities: []types.Capability{
				{
					Name:     "rerank",
					WorkUnit: "request",
					Offerings: []types.Offering{
						{ID: "zerank-2-default", PricePerWorkUnitWei: "1", Constraints: constraints},
					},
				},
			},
		}
		out, err := selectedRouteFromResolvedNode(in, selection.Filter{
			Capability: "rerank",
			Offering:   "zerank-2-default",
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	absent := build(nil)
	empty := build([]byte("{}"))
	whitespace := build([]byte("  \n\t"))

	if len(absent.ConstraintFingerprint) == 0 {
		t.Fatalf("absent constraints produced empty fingerprint")
	}
	if string(absent.ConstraintFingerprint) != string(empty.ConstraintFingerprint) {
		t.Fatalf("absent vs `{}` constraints fingerprint drift: %x vs %x",
			absent.ConstraintFingerprint, empty.ConstraintFingerprint)
	}
	if string(absent.ConstraintFingerprint) != string(whitespace.ConstraintFingerprint) {
		t.Fatalf("absent vs whitespace constraints fingerprint drift: %x vs %x",
			absent.ConstraintFingerprint, whitespace.ConstraintFingerprint)
	}

	populated := build([]byte(`{"tier":"standard"}`))
	if string(populated.ConstraintFingerprint) == string(absent.ConstraintFingerprint) {
		t.Fatalf("populated constraints collided with empty-object digest")
	}
}

func TestResolveResultToProto_NilSafe(t *testing.T) {
	if got := resolveResultToProto(nil); got == nil {
		t.Fatal("nil-safe expected non-nil empty proto")
	}
}

func TestResolveResultToProto_PopulatesFields(t *testing.T) {
	r := &types.ResolveResult{
		EthAddress:      "0xabcdef0000000000000000000000000000000000",
		ResolvedURI:     "https://x",
		Mode:            types.ModeWellKnown,
		FreshnessStatus: types.Fresh,
		SchemaVersion:   "3.0.1",
		CachedAt:        time.Unix(1745000000, 0).UTC(),
		FetchedAt:       time.Unix(1745000060, 0).UTC(),
		Nodes:           []types.ResolvedNode{{ID: "n", URL: "https://x"}},
	}
	out := resolveResultToProto(r)
	if out.GetEthAddress() != string(r.EthAddress) {
		t.Fatalf("eth drift: %s", out.GetEthAddress())
	}
	if out.GetMode() != registryv1.ResolveMode_RESOLVE_MODE_WELL_KNOWN {
		t.Fatalf("mode drift: %v", out.GetMode())
	}
	if len(out.GetNodes()) != 1 {
		t.Fatalf("nodes drift: %+v", out.GetNodes())
	}
	if out.GetCachedAt() == nil || out.GetFetchedAt() == nil {
		t.Fatal("timestamps not set")
	}
}

func TestAuditEventToProto(t *testing.T) {
	in := types.AuditEvent{
		At:         time.Unix(1745000000, 0).UTC(),
		EthAddress: "0xabcdef0000000000000000000000000000000000",
		Kind:       types.AuditManifestFetched,
		Mode:       types.ModeWellKnown,
		Detail:     "nodes=3",
	}
	out := auditEventToProto(in)
	if out.GetEthAddress() != string(in.EthAddress) {
		t.Fatalf("eth: %s", out.GetEthAddress())
	}
	if out.GetKind() != "manifest_fetched" {
		t.Fatalf("kind: %s", out.GetKind())
	}
	if out.GetDetail() != "nodes=3" {
		t.Fatalf("detail: %s", out.GetDetail())
	}
}

func TestTimeRoundTrip(t *testing.T) {
	if got := timeFromProto(timeToProto(time.Time{})); !got.IsZero() {
		t.Fatalf("zero time round-trip: %v", got)
	}
	now := time.Unix(1745000000, 0).UTC()
	if got := timeFromProto(timeToProto(now)); !got.Equal(now) {
		t.Fatalf("non-zero time round-trip: %v vs %v", got, now)
	}
}

func TestNodesFromProto_FiltersNil(t *testing.T) {
	in := []*registryv1.Node{
		nil,
		{Id: "n1", Url: "https://x", Capabilities: []*registryv1.Capability{{Name: "c1"}}},
	}
	out := nodesFromProto(in)
	if len(out) != 1 || out[0].ID != "n1" || len(out[0].Capabilities) != 1 {
		t.Fatalf("got %+v", out)
	}
}

func TestNodesFromProto_ManifestFields(t *testing.T) {
	in := []*registryv1.Node{
		{Id: "n", Url: "https://x", WorkerEthAddress: "0x1111111111111111111111111111111111111111", ExtraJson: []byte(`{"a":1}`)},
	}
	out := nodesFromProto(in)
	if out[0].WorkerEthAddress != "0x1111111111111111111111111111111111111111" {
		t.Fatalf("worker_eth_address: %+v", out[0].WorkerEthAddress)
	}
	if string(out[0].Extra) != `{"a":1}` {
		t.Fatalf("extra: %+v", string(out[0].Extra))
	}
}

// The estimator has to survive every hop between the signed manifest and
// the consumer, and it previously did not: it reached the route struct
// and died at the gRPC boundary, because the proto message had no field
// for it. A consumer that cannot read it cannot reserve funds for a
// multipart upload, so it guesses — and a guessed reservation and the
// seller's bill are two different numbers.
//
// This pins the whole projection rather than each hop, because every hop
// individually looked fine while the chain was broken.
func TestSelectedRouteCarriesEstimatorOntoTheWire(t *testing.T) {
	est := &types.Estimator{
		ID:        "multipart-audio-duration/v1",
		Rounding:  "ceil-to-whole-seconds",
		Exactness: "exact-or-reject",
		Package:   "@livepeer-network/audio-duration",
		Fixtures:  "livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1",
	}
	in := types.ResolvedNode{
		URL:          "https://worker.example.com",
		OperatorAddr: "0xabcdef0000000000000000000000000000000000",
		Capabilities: []types.Capability{{
			Name:              "openai:audio-transcriptions",
			WorkUnit:          "seconds",
			WorkUnitEstimator: est,
			Offerings:         []types.Offering{{ID: "default", PricePerWorkUnitWei: "100"}},
		}},
	}

	route, err := selectedRouteFromResolvedNode(in, selection.Filter{
		Capability: "openai:audio-transcriptions",
		Offering:   "default",
	})
	if err != nil {
		t.Fatal(err)
	}
	if route.WorkUnitEstimator == nil {
		t.Fatal("estimator dropped between resolved node and route")
	}

	wire := selectedRouteToProto(route)
	got := wire.GetWorkUnitEstimator()
	if got == nil {
		t.Fatal("estimator dropped at the gRPC boundary")
	}
	if got.GetId() != est.ID {
		t.Errorf("id = %q, want %q", got.GetId(), est.ID)
	}
	if got.GetRounding() != est.Rounding {
		t.Errorf("rounding = %q, want %q", got.GetRounding(), est.Rounding)
	}
	if got.GetExactness() != est.Exactness {
		t.Errorf("exactness = %q, want %q", got.GetExactness(), est.Exactness)
	}
	if got.GetPackage() != est.Package {
		t.Errorf("package = %q, want %q", got.GetPackage(), est.Package)
	}
	if got.GetFixtures() != est.Fixtures {
		t.Errorf("fixtures = %q, want %q", got.GetFixtures(), est.Fixtures)
	}
}

// Most capabilities have no estimator, and a nil one must stay nil
// rather than become an empty block a consumer would read as "an
// estimator I do not implement" and refuse the route over.
func TestSelectedRouteWithoutEstimatorStaysAbsent(t *testing.T) {
	in := types.ResolvedNode{
		URL:          "https://worker.example.com",
		OperatorAddr: "0xabcdef0000000000000000000000000000000000",
		Capabilities: []types.Capability{{
			Name:      "openai:/v1/chat/completions",
			WorkUnit:  "token",
			Offerings: []types.Offering{{ID: "gpt-oss-20b", PricePerWorkUnitWei: "1000"}},
		}},
	}
	route, err := selectedRouteFromResolvedNode(in, selection.Filter{
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-oss-20b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := selectedRouteToProto(route).GetWorkUnitEstimator(); got != nil {
		t.Fatalf("absent estimator materialized on the wire: %+v", got)
	}
}

// The node-listing path round-trips through proto too, and dropped the
// estimator in exactly the same way.
func TestCapabilityEstimatorSurvivesProtoRoundTrip(t *testing.T) {
	in := types.Capability{
		Name:     "openai:audio-transcriptions",
		WorkUnit: "seconds",
		WorkUnitEstimator: &types.Estimator{
			ID:        "multipart-audio-duration/v1",
			Rounding:  "ceil-to-whole-seconds",
			Exactness: "exact-or-reject",
		},
	}
	got := capabilityFromProto(capabilityToProto(in))
	if got.WorkUnitEstimator == nil {
		t.Fatal("estimator lost in the capability proto round trip")
	}
	if got.WorkUnitEstimator.ID != in.WorkUnitEstimator.ID ||
		got.WorkUnitEstimator.Rounding != in.WorkUnitEstimator.Rounding ||
		got.WorkUnitEstimator.Exactness != in.WorkUnitEstimator.Exactness {
		t.Fatalf("estimator drift: %+v", got.WorkUnitEstimator)
	}
	if capabilityFromProto(capabilityToProto(types.Capability{Name: "x"})).WorkUnitEstimator != nil {
		t.Fatal("absent estimator materialized in the round trip")
	}
}
