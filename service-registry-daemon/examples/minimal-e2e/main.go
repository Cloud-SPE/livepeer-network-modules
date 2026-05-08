// minimal-e2e demonstrates the full registry pipeline in-process.
// See examples/minimal-e2e/README.md for what it covers.
package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/runtime/grpc"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/publisher"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-rewrite/service-registry-daemon/internal/types"
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

	// 2. Publisher builds + signs a manifest covering AI + transcoding.
	pub := publisher.New(publisher.Config{
		Chain:  c,
		Signer: sk,
		Audit:  audit.New(store.NewMemory()),
		Clock:  clk,
	})
	m, err := pub.BuildManifest(publisher.BuildSpec{
		Nodes: []types.Node{
			{
				ID:  "ai-east",
				URL: "https://ai-east.example.com:8935",
				Capabilities: []types.Capability{
					{
						Name:      "openai:/v1/chat/completions",
						WorkUnit:  "token",
						Offerings: []types.Offering{{ID: "gpt-oss-20b", PricePerWorkUnitWei: "1000"}},
					},
					{
						Name:      "openai:/v1/embeddings",
						WorkUnit:  "token",
						Offerings: []types.Offering{{ID: "text-embedding-3-small", PricePerWorkUnitWei: "900"}},
					},
				},
			},
			{
				ID:  "ai-west",
				URL: "https://ai-west.example.com:8935",
				Capabilities: []types.Capability{
					{
						Name:      "openai:/v1/chat/completions",
						WorkUnit:  "token",
						Offerings: []types.Offering{{ID: "gpt-oss-20b", PricePerWorkUnitWei: "1100"}},
					},
				},
			},
			{
				ID:  "transcoder-1",
				URL: "https://orch.example.com:8935",
				Capabilities: []types.Capability{
					{
						Name:     "livepeer:transcoder/h264",
						WorkUnit: "frame",
						Offerings: []types.Offering{
							{ID: "h264-main", PricePerWorkUnitWei: "2000"},
						},
					},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	signed, err := pub.SignManifest(m)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(signed)
	fmt.Printf("Signed manifest: %d bytes, sig=%s...\n", len(body), signed.Signature.Value[:14])

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
		Capability: "openai:/v1/chat/completions",
		Offering:   "gpt-oss-20b",
	})
	fmt.Printf("Select(chat/completions, gpt-oss-20b): worker=%s recipient=%s price=%s/%s\n",
		chat.WorkerURL, chat.EthAddress, chat.PricePerWorkUnitWei, chat.WorkUnit)

	// Health probe.
	h := srv.Health(ctx)
	fmt.Println("Health:", h.String())
	return nil
}
