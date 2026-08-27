// minimal-e2e demonstrates the full registry pipeline in-process.
// See examples/minimal-e2e/README.md for what it covers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/chain"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/clock"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/manifestfetcher"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/signer"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/verifier"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("FAIL:", err)
		return
	}
	fmt.Println("OK — minimal-e2e completed")
}

func run() error {
	ctx := context.Background()
	clk := &clock.Fixed{T: time.Unix(1745000000, 0).UTC()}

	// 1. Make a key, build an in-memory chain.
	sk, err := signer.GenerateRandom()
	if err != nil {
		return err
	}
	addr := sk.Address()
	fmt.Println("Operator address:", addr)

	c := chain.NewInMemory(addr)
	uri := "https://orch.example.com/.well-known/livepeer-registry.json"
	c.PreLoad(addr, uri)

	// 2. A coordinator-shaped signed envelope. This is the only manifest
	// shape the daemon reads now (plan 0043 §3.8): orch-coordinator
	// builds it, the cold key on secure-orch signs it, and the daemon
	// resolves it. The daemon no longer builds or signs manifests —
	// doing so was a second signing path beside the cold key.
	env := types.CoordinatorSignedManifest{
		Manifest: types.CoordinatorManifestPayload{
			SpecVersion:    "2.4.1",
			PublicationSeq: 1,
			IssuedAt:       clk.Now().UTC(),
			ExpiresAt:      clk.Now().UTC().Add(24 * time.Hour),
			Orch:           types.CoordinatorOrch{EthAddress: string(addr), ServiceURI: uri},
			Capabilities: []types.CoordinatorCapability{
				{
					CapabilityID:    "openai:chat-completions",
					OfferingID:      "gpt-oss-20b",
					Protocol:        "paid-job/v1",
					Job:             json.RawMessage(`{"transports":["unary","stream"]}`),
					WorkUnit:        types.CoordinatorWorkUnit{Name: "tokens"},
					PricePerUnitWei: "1000",
					PerUnits:        1,
					WorkerURL:       "https://ai-east.example.com:8935",
				},
				{
					CapabilityID:    "livepeer:transcoder/h264",
					OfferingID:      "h264-main",
					Protocol:        "paid-job/v1",
					Job:             json.RawMessage(`{"transports":["unary"]}`),
					WorkUnit:        types.CoordinatorWorkUnit{Name: "frame"},
					PricePerUnitWei: "2000",
					PerUnits:        1,
					WorkerURL:       "https://transcode.example.com:8935",
				},
			},
		},
		Signature: types.CoordinatorEnvelopeSignature{
			Algorithm:        types.CoordinatorSignatureAlg,
			Canonicalization: "JCS",
		},
	}
	canonical, err := types.CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		return err
	}
	sig, err := sk.SignCanonical(canonical)
	if err != nil {
		return err
	}
	env.Signature.Value = "0x" + hexString(sig)
	body, err := json.Marshal(env)
	if err != nil {
		return err
	}
	fmt.Printf("Signed envelope: %d bytes, sig=%s...\n", len(body), env.Signature.Value[:14])

	// 3. Resolver against the same chain + a Static fetcher carrying our manifest.
	fetcher := &manifestfetcher.Static{
		Bodies: map[string][]byte{
			uri: body,
		},
	}
	kv := store.NewMemory()
	res := resolver.New(resolver.Config{
		Chain:    c,
		Fetcher:  fetcher,
		Verifier: verifier.New(),
		Cache:    manifestcache.New(kv),
		Audit:    audit.New(kv),
		Overlay:  func() *config.Overlay { return config.EmptyOverlay() },
		Clock:    clk,
	})
	srv, _ := grpc.NewServer(grpc.Config{Resolver: res, Cache: manifestcache.New(kv), Audit: audit.New(kv)})

	// 4. Consumer: ResolveByAddress.
	out, err := srv.ResolveByAddress(ctx, grpc.ResolveByAddressRequest{EthAddress: string(addr)})
	if err != nil {
		return err
	}
	fmt.Printf("Resolve: mode=%s nodes=%d\n", out.Mode, len(out.Nodes))
	for _, n := range out.Nodes {
		caps := make([]string, 0, len(n.Capabilities))
		for _, c := range n.Capabilities {
			caps = append(caps, c.Name)
		}
		fmt.Printf("  - %s @ %s sig=%s caps=%v\n", n.ID, n.URL, n.SignatureStatus, caps)
	}

	// Select transcoder route.
	tx, _ := srv.Select(ctx, grpc.SelectRequest{
		Capability: "livepeer:transcoder/h264",
		Offering:   "h264-main",
	})
	fmt.Printf("Select(transcoder/h264, h264-main): worker=%s recipient=%s price=%s/%s\n",
		tx.WorkerURL, tx.EthAddress, tx.PricePerWorkUnitWei, tx.WorkUnit)

	// Select chat route.
	chat, _ := srv.Select(ctx, grpc.SelectRequest{
		Capability: "openai:chat-completions",
		Offering:   "gpt-oss-20b",
	})
	fmt.Printf("Select(chat/completions, gpt-oss-20b): worker=%s recipient=%s price=%s/%s\n",
		chat.WorkerURL, chat.EthAddress, chat.PricePerWorkUnitWei, chat.WorkUnit)

	// Health probe.
	h := srv.Health(ctx)
	fmt.Println("Health:", h.String())
	return nil
}

// hexString renders signature bytes for display.
func hexString(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&0x0f])
	}
	return string(out)
}
