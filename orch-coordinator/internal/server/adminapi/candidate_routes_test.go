package adminapi

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/providers/brokerclient"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/repo/candidates"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/candidate"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func TestCandidateRoutes_NotReadyReturns503(t *testing.T) {
	dir := t.TempDir()
	store, err := candidates.New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	scrapeSvc := emptyScrapeService(t)
	builder, err := candidate.NewBuilder(scrapeSvc, store, candidate.BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    time.Hour,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}

	srv := New("127.0.0.1:0", slog.Default())
	srv.CandidateRoutes(builder, store)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	resp, err := http.Get("http://" + srv.Addr() + "/candidate.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
}

func TestCandidateRoutes_ReturnsJSONAndTarballAfterBuild(t *testing.T) {
	dir := t.TempDir()
	store, err := candidates.New(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	scrapeSvc := primedScrapeService(t)
	builder, err := candidate.NewBuilder(scrapeSvc, store, candidate.BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    time.Hour,
	}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Rebuild(); err != nil {
		t.Fatal(err)
	}

	srv := New("127.0.0.1:0", slog.Default())
	srv.CandidateRoutes(builder, store)
	if _, err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.Serve(ctx)

	for _, path := range []string{"/candidate.json", "/candidate.tar.gz"} {
		u := url.URL{Scheme: "http", Host: srv.Addr(), Path: path}
		resp, err := http.Get(u.String())
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", path, resp.StatusCode, body)
		}
		if len(body) == 0 {
			t.Fatalf("%s: empty body", path)
		}
	}
}

func emptyScrapeService(t *testing.T) *scrape.Service {
	t.Helper()
	fc := brokerclient.NewFake()
	svc, err := scrape.New(scrape.Config{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: time.Second,
		ScrapeTimeout:  time.Second,
	}, fc, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func primedScrapeService(t *testing.T) *scrape.Service {
	t.Helper()
	fc := brokerclient.NewFake()
	addr := "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	fc.Set("http://x:1", &types.BrokerOfferings{
		OrchEthAddress: addr,
		Capabilities: []types.BrokerOffering{{
			CapabilityID:    "cap",
			OfferingID:      "off",
			InteractionMode: "http-stream@v1",
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "100",
		}},
	}, nil)
	svc, err := scrape.New(scrape.Config{
		OrchEthAddress: addr,
		Brokers:        []config.Broker{{Name: "b1", BaseURL: "http://x:1"}},
		ScrapeInterval: time.Second,
		ScrapeTimeout:  time.Second,
		WorkerURLOverride: map[string]string{
			"b1": "https://b1.example/",
		},
	}, fc, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	svc.ScrapeOnce(context.Background())
	return svc
}

