package grpc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/providers/store"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/repo/manifestcache"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/service/resolver"
	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

// BenchmarkSelectMany includes route/quote construction and fingerprints, with
// 1,000 warmed routes over 100 tuples and 10 matches/request. It excludes gRPC
// transport and remote refresh; there are no network providers to call.
func BenchmarkSelectMany(b *testing.B) {
	const addr = types.EthAddress("0x1111111111111111111111111111111111111111")
	var yaml strings.Builder
	fmt.Fprintf(&yaml, "overlay:\n  - eth_address: %q\n    unsigned_allowed: true\n    pin:\n", addr)
	for i := 0; i < 1000; i++ {
		fmt.Fprintf(&yaml, "      - id: broker-%d\n        url: https://broker-%d.example.com\n        capabilities:\n          - name: cap:%d\n            protocol: paid-job/v1\n            work_unit: unit\n            offerings:\n              - id: default\n                price_per_work_unit_wei: \"10\"\n", i, i, i%100)
	}
	overlay, err := config.ParseOverlayYAML([]byte(yaml.String()))
	if err != nil {
		b.Fatal(err)
	}
	cache := manifestcache.New(store.NewMemory())
	r := resolver.New(resolver.Config{Cache: cache, Overlay: func() *config.Overlay { return overlay }, OverlayOnly: true})
	if err := r.RefreshAddress(context.Background(), addr); err != nil {
		b.Fatal(err)
	}
	server, err := NewServer(Config{Resolver: r, Cache: cache})
	if err != nil {
		b.Fatal(err)
	}
	req := SelectRequest{Capability: "cap:1", Offering: "default"}
	var mu sync.Mutex
	var samples []int64
	b.ResetTimer()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		var local []int64
		for pb.Next() {
			start := time.Now()
			routes, err := server.SelectMany(context.Background(), req)
			if err != nil || len(routes) != 10 {
				b.Fatalf("routes=%d err=%v", len(routes), err)
			}
			if len(local) < 4096 {
				local = append(local, time.Since(start).Nanoseconds())
			}
		}
		mu.Lock()
		samples = append(samples, local...)
		mu.Unlock()
	})
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	if len(samples) > 0 {
		b.ReportMetric(float64(samples[(len(samples)-1)*99/100])/1e6, "sample-p99-ms")
	}
}
