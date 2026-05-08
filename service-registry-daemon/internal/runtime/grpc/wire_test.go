package grpc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	registryv1 "github.com/Cloud-SPE/livepeer-network-rewrite/proto-contracts/livepeer/registry/v1"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/logger"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

// wireFixture spins up a real *grpc.Server in-process via bufconn
// (no socket; same wire path) with a resolver mounted, an in-memory
// chain seeded with one orchestrator address, and a Static fetcher
// pre-populated with a signed manifest.
type wireFixture struct {
	t          *testing.T
	addr       types.EthAddress
	signerKey  *signer.Keystore
	uri        string
	chain      *chain.InMemory
	fetcher    *manifestfetcher.Static
	overlay    *config.Overlay
	cache      manifestcache.Repo
	auditRepo  audit.Repo
	clk        *clock.Fixed
	server     *Server
	bufLn      *bufconn.Listener
	gsrv       *grpc.Server
	clientConn *grpc.ClientConn
}

func newWireFixture(t *testing.T) *wireFixture {
	t.Helper()
	sk, err := signer.GenerateRandom()
	if err != nil {
		t.Fatal(err)
	}
	addr := sk.Address()

	c := chain.NewInMemory(addr)
	uri := "https://orch.example.com/.well-known/livepeer-registry.json"
	c.PreLoad(addr, uri)

	fetcher := &manifestfetcher.Static{Bodies: map[string][]byte{}}
	overlay := config.EmptyOverlay()
	kv := store.NewMemory()
	cacheRepo := manifestcache.New(kv)
	auditRepo := audit.New(kv)
	clk := &clock.Fixed{T: time.Unix(1745000000, 0).UTC()}

	r := resolver.New(resolver.Config{
		Chain:    c,
		Fetcher:  fetcher,
		Verifier: verifier.New(),
		Cache:    cacheRepo,
		Audit:    auditRepo,
		Overlay:  func() *config.Overlay { return overlay },
		Clock:    clk,
	})
	pub := publisher.New(publisher.Config{Chain: c, Signer: sk, Audit: auditRepo, Clock: clk})

	srv, err := NewServer(Config{
		Resolver:  r,
		Publisher: pub,
		Cache:     cacheRepo,
		Audit:     auditRepo,
		Logger:    logger.Discard(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// Build the gRPC server using the production listener-construction
	// path (interceptors, registrations) but back it with a bufconn
	// listener instead of a unix socket.
	bufLn := bufconn.Listen(1 << 20)
	gsrv := grpc.NewServer(
		grpc.UnaryInterceptor(chainInterceptors(
			recoverInterceptor(logger.Discard()),
			deadlineInterceptor(5*time.Second),
			loggingInterceptor(logger.Discard(), "test"),
		)),
	)
	registryv1.RegisterResolverServer(gsrv, newResolverAdapter(srv))
	registryv1.RegisterPublisherServer(gsrv, newPublisherAdapter(srv))
	hs := newHealthService()
	hs.SetServing(true)
	healthpb.RegisterHealthServer(gsrv, hs)
	go func() { _ = gsrv.Serve(bufLn) }()
	t.Cleanup(func() { gsrv.GracefulStop() })

	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return bufLn.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return &wireFixture{
		t:          t,
		addr:       addr,
		signerKey:  sk,
		uri:        uri,
		chain:      c,
		fetcher:    fetcher,
		overlay:    overlay,
		cache:      cacheRepo,
		auditRepo:  auditRepo,
		clk:        clk,
		server:     srv,
		bufLn:      bufLn,
		gsrv:       gsrv,
		clientConn: conn,
	}
}

// signManifestForFixture builds + hosts a signed manifest at f.uri.
func (f *wireFixture) signManifestForFixture(nodes []types.Node) {
	f.t.Helper()
	m := &types.Manifest{
		SchemaVersion: types.SchemaVersion,
		EthAddress:    string(f.addr),
		IssuedAt:      f.clk.Now(),
		Nodes:         nodes,
		Signature:     types.Signature{Alg: types.SignatureAlgEthPersonal},
	}
	canonical, err := types.CanonicalBytes(m)
	if err != nil {
		f.t.Fatal(err)
	}
	sig, err := f.signerKey.SignCanonical(canonical)
	if err != nil {
		f.t.Fatal(err)
	}
	m.Signature.Value = "0x" + hexLower(sig)
	m.Signature.SignedCanonicalBytesSHA256 = types.CanonicalSHA256(canonical)
	body, err := json.Marshal(m)
	if err != nil {
		f.t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = body
}

func TestWire_ResolveByAddress_HappyPath(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{
		{
			ID:  "n1",
			URL: "https://orch.example.com:8935",
			Capabilities: []types.Capability{
				{Name: "openai:/v1/chat/completions", WorkUnit: "token"},
			},
		},
	})
	cli := registryv1.NewResolverClient(f.clientConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)})
	if err != nil {
		t.Fatalf("RPC error: %v", err)
	}
	if res.GetMode() != registryv1.ResolveMode_RESOLVE_MODE_WELL_KNOWN {
		t.Fatalf("mode: %v", res.GetMode())
	}
	if len(res.GetNodes()) != 1 {
		t.Fatalf("nodes: %d", len(res.GetNodes()))
	}
	n := res.GetNodes()[0]
	if n.GetSignatureStatus() != registryv1.SignatureStatus_SIGNATURE_STATUS_VERIFIED {
		t.Fatalf("sig status: %v", n.GetSignatureStatus())
	}
	if n.GetCapabilities()[0].GetName() != "openai:/v1/chat/completions" {
		t.Fatalf("capability name: %s", n.GetCapabilities()[0].GetName())
	}
}

func TestWire_ResolveByAddress_NotFound_CarriesStableCode(t *testing.T) {
	f := newWireFixture(t)
	cli := registryv1.NewResolverClient(f.clientConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := cli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{
		EthAddress: "0xfedcba0000000000000000000000000000000000",
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %s", status.Code(err))
	}
	if got := extractCode(err); got != "not_found" {
		t.Fatalf("registry code = %q", got)
	}
}

func TestWire_BadEthAddress_CarriesParseCode(t *testing.T) {
	f := newWireFixture(t)
	cli := registryv1.NewResolverClient(f.clientConn)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := cli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{EthAddress: "not-an-address"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s", status.Code(err))
	}
	if got := extractCode(err); got != "parse_error" {
		t.Fatalf("registry code = %q", got)
	}
}

func TestWire_Select_FilterByCapability(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{
		{ID: "n1", URL: "https://orch.example.com:8935", Capabilities: []types.Capability{{Name: "openai:/v1/chat/completions", WorkUnit: "token", Offerings: []types.Offering{{ID: "gpt-oss-20b", PricePerWorkUnitWei: "1000"}}}}},
		{ID: "n2", URL: "https://orch.example.com:8936", Capabilities: []types.Capability{{Name: "livepeer:transcoder/h264", WorkUnit: "frame", Offerings: []types.Offering{{ID: "h264-main", PricePerWorkUnitWei: "2000"}}}}},
	})
	// Prime the cache.
	if _, err := registryv1.NewResolverClient(f.clientConn).ResolveByAddress(context.Background(),
		&registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)}); err != nil {
		t.Fatal(err)
	}
	cli := registryv1.NewResolverClient(f.clientConn)
	res, err := cli.Select(context.Background(), &registryv1.SelectRequest{
		Capability: "livepeer:transcoder/h264",
		Offering:   "h264-main",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetRoute().GetWorkerUrl() != "https://orch.example.com:8936" {
		t.Fatalf("worker_url: %+v", res.GetRoute())
	}
	if res.GetRoute().GetEthAddress() != string(f.addr) {
		t.Fatalf("eth_address: %+v", res.GetRoute())
	}
	if res.GetRoute().GetPricePerWorkUnitWei() != "2000" || res.GetRoute().GetWorkUnit() != "frame" {
		t.Fatalf("pricing fields: %+v", res.GetRoute())
	}
}

func TestWire_Health(t *testing.T) {
	f := newWireFixture(t)
	cli := healthpb.NewHealthClient(f.clientConn)
	res, err := cli.Check(context.Background(), &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("status: %v", res.GetStatus())
	}
}

func TestWire_Publisher_BuildAndSign(t *testing.T) {
	f := newWireFixture(t)
	pcli := registryv1.NewPublisherClient(f.clientConn)
	ctx := context.Background()

	identity, err := pcli.GetIdentity(ctx, nil)
	if err != nil {
		t.Fatalf("GetIdentity: %v", err)
	}
	if identity.GetEthAddress() != string(f.addr) {
		t.Fatalf("identity = %s, want %s", identity.GetEthAddress(), f.addr)
	}

	build, err := pcli.BuildManifest(ctx, &registryv1.BuildManifestRequest{
		ProposedEthAddress: string(f.addr),
		ProposedNodes: []*registryv1.Node{
			{Id: "n1", Url: "https://orch.example.com:8935", Capabilities: []*registryv1.Capability{{Name: "openai:/v1/chat/completions"}}},
		},
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(build.GetManifestJson()) == 0 || len(build.GetCanonicalBytes()) == 0 {
		t.Fatal("Build produced empty bytes")
	}

	signed, err := pcli.SignManifest(ctx, &registryv1.SignManifestRequest{ManifestJson: build.GetManifestJson()})
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if signed.GetSignatureValue() == "" || len(signed.GetSignatureValue()) != 132 {
		t.Fatalf("signature shape: %s", signed.GetSignatureValue())
	}

	// Decode-and-validate the signed body via DecodeManifest to confirm
	// the signature recovers to the publisher's address.
	m, err := types.DecodeManifest(signed.GetManifestJson())
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if m.EthAddress != string(f.addr) {
		t.Fatalf("eth: %s vs %s", m.EthAddress, f.addr)
	}
}

func TestWire_Refresh_ListKnown(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{}}})
	cli := registryv1.NewResolverClient(f.clientConn)
	ctx := context.Background()
	if _, err := cli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Refresh(ctx, &registryv1.RefreshRequest{EthAddress: string(f.addr), Force: true}); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	known, err := cli.ListKnown(ctx, &registryv1.ListKnownRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(known.GetEntries()) != 1 {
		t.Fatalf("known entries: %d", len(known.GetEntries()))
	}
}

func TestWire_GetAuditLog(t *testing.T) {
	f := newWireFixture(t)
	f.signManifestForFixture([]types.Node{{ID: "n1", URL: "https://x.test", Capabilities: []types.Capability{}}})
	cli := registryv1.NewResolverClient(f.clientConn)
	ctx := context.Background()
	if _, err := cli.ResolveByAddress(ctx, &registryv1.ResolveByAddressRequest{EthAddress: string(f.addr)}); err != nil {
		t.Fatal(err)
	}
	res, err := cli.GetAuditLog(ctx, &registryv1.GetAuditLogRequest{EthAddress: string(f.addr)})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetEvents()) == 0 {
		t.Fatal("expected at least one audit event")
	}
}

// TestUnixSocketLifecycle exercises the real Listener + GracefulStop +
// socket cleanup path against a real unix socket file in a tmpdir.
func TestUnixSocketLifecycle(t *testing.T) {
	tmp := t.TempDir()
	sockPath := filepath.Join(tmp, "registry.sock")

	srv := mustNewMinimalServer(t)
	ln, err := NewListener(ListenerConfig{
		SocketPath: sockPath,
		Server:     srv,
		Logger:     logger.New(logger.Config{Level: "debug", Format: "text"}),
		Version:    "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ln.Serve(ctx) }()

	// Wait for the socket to appear (Serve binds before listening
	// loop). Poll briefly.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := net.Dial("unix", sockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket never appeared")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Connect via the unix socket and run a Health check.
	conn, err := grpc.NewClient(
		"unix://"+sockPath,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	hcli := healthpb.NewHealthClient(conn)
	cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer ccancel()
	res, err := hcli.Check(cctx, &healthpb.HealthCheckRequest{})
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if res.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		t.Fatalf("not serving: %v", res.GetStatus())
	}
	// Close the client connection before cancelling so GracefulStop
	// has nothing to drain.
	_ = conn.Close()

	// Trigger shutdown via context, then explicitly call Stop as a
	// belt-and-suspenders against any client keepalive that resists
	// graceful drain. Listener.Stop is idempotent.
	cancel()
	go ln.Stop()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve: %v", err)
		}
	case <-time.After(8 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	// Socket file should be cleaned up.
	if _, err := net.Dial("unix", sockPath); err == nil {
		t.Fatal("socket still accepting connections after Stop")
	}
}

func TestPrepSocketPath_RefusesRegularFile(t *testing.T) {
	tmp := t.TempDir()
	regular := filepath.Join(tmp, "not-a-socket")
	if err := writeFile(regular, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := prepSocketPath(regular); err == nil {
		t.Fatal("expected refusal on non-socket")
	}
}

func TestPrepSocketPath_NoExistOK(t *testing.T) {
	if err := prepSocketPath(filepath.Join(t.TempDir(), "fresh.sock")); err != nil {
		t.Fatalf("expected nil for non-existent path, got %v", err)
	}
}

// helpers

func mustNewMinimalServer(t *testing.T) *Server {
	t.Helper()
	sk, _ := signer.GenerateRandom()
	pub := publisher.New(publisher.Config{Chain: chain.NewInMemory(sk.Address()), Signer: sk, Audit: nil, Clock: clock.System{}})
	srv, err := NewServer(Config{Publisher: pub, Logger: logger.Discard()})
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func writeFile(path string, b []byte) error {
	return os.WriteFile(path, b, 0o600)
}

func hexLower(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, c := range b {
		out[i*2] = digits[c>>4]
		out[i*2+1] = digits[c&0x0f]
	}
	return string(out)
}
