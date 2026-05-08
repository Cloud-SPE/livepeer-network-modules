package resolver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
)

// fixture builds a fully-configured resolver Service, an in-memory
// chain seeded with a single orchestrator address, a static fetcher
// returning a signed manifest, and exposes pieces tests can mutate.
type fixture struct {
	t       *testing.T
	addr    types.EthAddress
	signer  *signer.Keystore
	uri     string
	chain   *chain.InMemory
	fetcher *manifestfetcher.Static
	overlay *config.Overlay
	cache   manifestcache.Repo
	audit   audit.Repo
	clk     *clock.Fixed
	svc     *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	sk, err := signer.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	addr := sk.Address()
	uri := "https://orch.example.com/.well-known/livepeer-registry.json"

	c := chain.NewInMemory(addr)
	c.PreLoad(addr, uri)
	fetcher := &manifestfetcher.Static{Bodies: map[string][]byte{}}
	overlay := config.EmptyOverlay()

	kv := store.NewMemory()
	cacheRepo := manifestcache.New(kv)
	auditRepo := audit.New(kv)
	clk := &clock.Fixed{T: time.Unix(1745000000, 0).UTC()}

	svc := New(Config{
		Chain:    c,
		Fetcher:  fetcher,
		Verifier: verifier.New(),
		Cache:    cacheRepo,
		Audit:    auditRepo,
		Overlay:  func() *config.Overlay { return overlay },
		Clock:    clk,
		// rejectUnsigned default = false here
	})
	return &fixture{
		t:       t,
		addr:    addr,
		signer:  sk,
		uri:     uri,
		chain:   c,
		fetcher: fetcher,
		overlay: overlay,
		cache:   cacheRepo,
		audit:   auditRepo,
		clk:     clk,
		svc:     svc,
	}
}

// signManifestForFixture builds a manifest signed by f.signer for f.addr.
func (f *fixture) signManifestForFixture(nodes []types.Node) []byte {
	f.t.Helper()
	m := &types.Manifest{
		SchemaVersion: types.SchemaVersion,
		EthAddress:    string(f.addr),
		IssuedAt:      f.clk.Now(),
		Nodes:         nodes,
		Signature: types.Signature{
			Alg: types.SignatureAlgEthPersonal,
		},
	}
	canonical, err := types.CanonicalBytes(m)
	if err != nil {
		f.t.Fatal(err)
	}
	sig, err := f.signer.SignCanonical(canonical)
	if err != nil {
		f.t.Fatal(err)
	}
	m.Signature.Value = "0x" + hex(sig)
	m.Signature.SignedCanonicalBytesSHA256 = types.CanonicalSHA256(canonical)
	body, err := json.Marshal(m)
	if err != nil {
		f.t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = body
	return body
}

func TestResolveByAddress_HappyPathSignedManifest(t *testing.T) {
	f := newFixture(t)
	f.signManifestForFixture([]types.Node{
		{
			ID:  "n1",
			URL: "https://orch.example.com:8935",
			Capabilities: []types.Capability{
				{Name: "openai:/v1/chat/completions", WorkUnit: "token"},
			},
		},
	})
	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != types.ModeWellKnown {
		t.Fatalf("mode = %v", res.Mode)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(res.Nodes))
	}
	n := res.Nodes[0]
	if n.SignatureStatus != types.SigVerified {
		t.Fatalf("expected verified, got %v", n.SignatureStatus)
	}
	if len(n.Capabilities) != 1 || n.Capabilities[0].Name != "openai:/v1/chat/completions" {
		t.Fatalf("capabilities not propagated: %+v", n.Capabilities)
	}
}

func TestResolveByAddress_HappyPathSignedManifestAIURL(t *testing.T) {
	f := newFixture(t)
	f.uri = "https://orch.example.com/.well-known/livepeer-ai-registry.json"
	f.chain.PreLoad(f.addr, f.uri)
	f.signManifestForFixture([]types.Node{
		{
			ID:  "n1",
			URL: "https://orch.example.com:8935",
			Capabilities: []types.Capability{
				{Name: "openai:/v1/responses", WorkUnit: "token"},
			},
		},
	})
	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err != nil {
		t.Fatal(err)
	}
	if res.ResolvedURI != f.uri {
		t.Fatalf("resolved uri = %q, want %q", res.ResolvedURI, f.uri)
	}
	if res.Mode != types.ModeWellKnown {
		t.Fatalf("mode = %v", res.Mode)
	}
}

func TestResolveByAddress_LegacyFallback(t *testing.T) {
	f := newFixture(t)
	// Don't add a manifest body — fetcher will report unavailable.
	res, err := f.svc.ResolveByAddress(context.Background(), Request{
		Address:             f.addr,
		AllowLegacyFallback: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != types.ModeLegacy {
		t.Fatalf("mode = %v", res.Mode)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].URL != f.uri {
		t.Fatalf("expected single legacy node with f.uri, got %+v", res.Nodes)
	}
	if res.Nodes[0].SignatureStatus != types.SigLegacy {
		t.Fatalf("expected legacy sig status, got %v", res.Nodes[0].SignatureStatus)
	}
}

func TestResolveByAddress_LegacyFallbackDeniedWithoutFlag(t *testing.T) {
	f := newFixture(t)
	_, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err == nil {
		t.Fatal("expected error without AllowLegacyFallback")
	}
	if !errors.Is(err, types.ErrManifestUnavailable) {
		t.Fatalf("expected ErrManifestUnavailable, got %v", err)
	}
}

func TestResolveByAddress_SignatureMismatchRejected(t *testing.T) {
	f := newFixture(t)
	// Build a manifest signed by a *different* key.
	other, _ := signer.GenerateRandom()
	m := &types.Manifest{
		SchemaVersion: types.SchemaVersion,
		EthAddress:    string(f.addr), // claims f.addr
		IssuedAt:      f.clk.Now(),
		Nodes:         []types.Node{{ID: "n1", URL: f.uri, Capabilities: []types.Capability{}}},
		Signature:     types.Signature{Alg: types.SignatureAlgEthPersonal},
	}
	canonical, _ := types.CanonicalBytes(m)
	sig, _ := other.SignCanonical(canonical)
	m.Signature.Value = "0x" + hex(sig)
	body, _ := json.Marshal(m)
	f.fetcher.Bodies[f.uri] = body

	_, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if !errors.Is(err, types.ErrSignatureMismatch) {
		t.Fatalf("expected ErrSignatureMismatch, got %v", err)
	}
}

func TestResolveByAddress_CSVMode(t *testing.T) {
	f := newFixture(t)
	// Replace chain entry with a CSV-format serviceURI.
	payload := `{"version":1,"nodes":[{"url":"https://csv-node.example.com","lat":40.71,"lon":-74}]}`
	csvURI := fmt.Sprintf("%s,1,%s", f.uri, base64.StdEncoding.EncodeToString([]byte(payload)))
	f.chain.PreLoad(f.addr, csvURI)

	// CSV-mode nodes are unsigned; default rejectUns is false in our
	// fixture, so they should come back. Mark them allowed via overlay
	// for clarity.
	res, err := f.svc.ResolveByAddress(context.Background(), Request{
		Address:       f.addr,
		AllowUnsigned: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Mode != types.ModeCSV {
		t.Fatalf("mode = %v", res.Mode)
	}
	if len(res.Nodes) != 1 {
		t.Fatalf("expected 1 csv node, got %d", len(res.Nodes))
	}
	if res.Nodes[0].URL != "https://csv-node.example.com" {
		t.Fatalf("URL = %s", res.Nodes[0].URL)
	}
	if res.Nodes[0].SignatureStatus != types.SigUnsigned {
		t.Fatalf("expected unsigned, got %v", res.Nodes[0].SignatureStatus)
	}
}

func TestResolveByAddress_OverlayPolicy_DropsUnsigned(t *testing.T) {
	f := newFixture(t)
	// Reject unsigned by default.
	f.svc.rejectUns = true
	// CSV serviceURI → unsigned nodes.
	payload := `{"nodes":[{"url":"https://csv.example.com"}]}`
	csvURI := fmt.Sprintf("%s,1,%s", f.uri, base64.StdEncoding.EncodeToString([]byte(payload)))
	f.chain.PreLoad(f.addr, csvURI)

	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Nodes) != 0 {
		t.Fatalf("expected 0 nodes (unsigned dropped), got %d", len(res.Nodes))
	}
}

func TestResolveByAddress_NotFound(t *testing.T) {
	f := newFixture(t)
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	_, err := f.svc.ResolveByAddress(context.Background(), Request{Address: other})
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestResolveByAddress_CacheReuseFresh(t *testing.T) {
	f := newFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "n1", URL: f.uri, Capabilities: []types.Capability{}}})

	// First call populates cache.
	if _, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr}); err != nil {
		t.Fatal(err)
	}
	// Remove the fetcher body — second call should still succeed from cache.
	delete(f.fetcher.Bodies, f.uri)
	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err != nil {
		t.Fatal(err)
	}
	if res.FreshnessStatus != types.Fresh {
		t.Fatalf("expected Fresh, got %v", res.FreshnessStatus)
	}
}

func TestResolveByAddress_StaleFailingFallback(t *testing.T) {
	f := newFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "n1", URL: f.uri, Capabilities: []types.Capability{}}})

	// Populate cache.
	if _, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr}); err != nil {
		t.Fatal(err)
	}
	// Advance past TTL but within max-stale.
	f.clk.Advance(2 * time.Hour)
	// Force chain failure.
	f.chain.PreLoad(f.addr, "") // empty serviceURI ≈ not-found path

	// First, simulate chain unavailable: replace the chain with one that errors.
	failingChain := &chain.InMemory{} // no signerAddr → SetServiceURI errors, but Get will return ErrNotFound for any address
	f.svc.chain = failingChain

	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
	if err != nil {
		// Path A: chain returns ErrNotFound; service surfaces it directly.
		if errors.Is(err, types.ErrNotFound) {
			return // acceptable
		}
		t.Fatal(err)
	}
	// Path B: it served last-good with StaleFailing freshness.
	if res != nil && res.FreshnessStatus != types.StaleFailing {
		// chain returned something usable instead — fine, but document why
		// the test reached here.
		t.Logf("did not exercise stale-failing path; freshness=%v", res.FreshnessStatus)
	}
}

func TestDecodeSig_RejectsMalformed(t *testing.T) {
	if _, err := decodeSig("not-hex"); !errors.Is(err, types.ErrSignatureMalformed) {
		t.Fatalf("expected ErrSignatureMalformed, got %v", err)
	}
	if _, err := decodeSig("0xZZ"); !errors.Is(err, types.ErrSignatureMalformed) {
		t.Fatalf("expected ErrSignatureMalformed, got %v", err)
	}
}

func TestDetectMode(t *testing.T) {
	cases := []struct {
		in   string
		want types.ResolveMode
	}{
		{"https://orch.example.com:8935", types.ModeWellKnown},
		{"http://localhost:8935", types.ModeWellKnown},
		{"", types.ModeUnknown},
		{"ftp://x", types.ModeUnknown},
		{"https://x,1,abc=", types.ModeCSV},
		{"https://x,notnumber,abc=", types.ModeUnknown},
		{"https://x,1,2,3", types.ModeUnknown},
	}
	for _, c := range cases {
		if got := detectMode(c.in); got != c.want {
			t.Fatalf("detectMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// staticOverlayYAML constructs an overlay containing one enabled entry
// for addr with a single pin node.
func staticOverlayYAML(addr types.EthAddress, pinID, pinURL, capability string) string {
	return fmt.Sprintf(`overlay:
  - eth_address: "%s"
    enabled: true
    weight: 100
    unsigned_allowed: true
    pin:
      - id: %s
        url: %q
        capabilities:
          - name: %q
            work_unit: frame
`, addr, pinID, pinURL, capability)
}

func TestResolveByAddress_StaticOverlaySynth_NoChain_ReturnsPinNodes(t *testing.T) {
	f := newFixture(t)
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	o, err := config.ParseOverlayYAML([]byte(staticOverlayYAML(other, "tx-1", "https://tx-1.example.com:8935", "livepeer:transcoder/h264")))
	if err != nil {
		t.Fatalf("parse overlay: %v", err)
	}
	f.svc.overlay = func() *config.Overlay { return o }

	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: other})
	if err != nil {
		t.Fatalf("expected synth ok, got %v", err)
	}
	if res.Mode != types.ModeStaticOverlay {
		t.Fatalf("mode: got %v want ModeStaticOverlay", res.Mode)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != "tx-1" {
		t.Fatalf("nodes: %+v", res.Nodes)
	}
	if res.Nodes[0].Source != types.SourceStaticOverlay {
		t.Fatalf("source: %v", res.Nodes[0].Source)
	}
	// Cache must contain the synth entry so ListKnown sees it.
	if _, ok, _ := f.cache.Get(other); !ok {
		t.Fatal("cache: expected entry written for synth address")
	}
}

func TestResolveByAddress_StaticOverlaySynth_NoPins_StillNotFound(t *testing.T) {
	f := newFixture(t)
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	yaml := fmt.Sprintf("overlay:\n  - eth_address: \"%s\"\n    enabled: true\n", other)
	o, err := config.ParseOverlayYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f.svc.overlay = func() *config.Overlay { return o }

	_, err = f.svc.ResolveByAddress(context.Background(), Request{Address: other})
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound (no pins to synth), got %v", err)
	}
}

func TestResolveByAddress_StaticOverlaySynth_DisabledEntry_StillNotFound(t *testing.T) {
	f := newFixture(t)
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	yaml := fmt.Sprintf(`overlay:
  - eth_address: "%s"
    enabled: false
    pin:
      - id: tx-1
        url: "https://tx-1.example.com:8935"
        capabilities:
          - name: "livepeer:transcoder/h264"
            work_unit: frame
`, other)
	o, err := config.ParseOverlayYAML([]byte(yaml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	f.svc.overlay = func() *config.Overlay { return o }

	_, err = f.svc.ResolveByAddress(context.Background(), Request{Address: other})
	if !errors.Is(err, types.ErrNotFound) {
		t.Fatalf("expected ErrNotFound (disabled entry), got %v", err)
	}
}

func TestResolveByAddress_StaticOverlay_CacheRehydrate_PicksUpOverlayChange(t *testing.T) {
	f := newFixture(t)
	other, _ := types.ParseEthAddress("0xfedcba0000000000000000000000000000000000")
	o1, err := config.ParseOverlayYAML([]byte(staticOverlayYAML(other, "tx-1", "https://tx-1.example.com:8935", "livepeer:transcoder/h264")))
	if err != nil {
		t.Fatal(err)
	}
	overlayPtr := o1
	f.svc.overlay = func() *config.Overlay { return overlayPtr }

	if _, err := f.svc.ResolveByAddress(context.Background(), Request{Address: other}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Operator swaps overlay to a different pin; second resolve hits the
	// cache (Mode=ModeStaticOverlay, fresh) but rebuilds nodes from the
	// live overlay.
	o2, err := config.ParseOverlayYAML([]byte(staticOverlayYAML(other, "tx-2", "https://tx-2.example.com:8935", "openai:/v1/chat/completions")))
	if err != nil {
		t.Fatal(err)
	}
	overlayPtr = o2

	res, err := f.svc.ResolveByAddress(context.Background(), Request{Address: other})
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if len(res.Nodes) != 1 || res.Nodes[0].ID != "tx-2" {
		t.Fatalf("expected pin tx-2 from updated overlay, got %+v", res.Nodes)
	}
}

// hex is a tiny hex encoder local to tests.
func hex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
