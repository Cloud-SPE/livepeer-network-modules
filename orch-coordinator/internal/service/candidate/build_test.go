package candidate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-rewrite/orch-coordinator/internal/types"
)

func sampleSnap() scrape.Snapshot {
	now := mustTime("2026-05-06T12:00:00Z")
	return scrape.Snapshot{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WindowStart:    now.Add(-30 * time.Second),
		WindowEnd:      now,
		Brokers: []scrape.BrokerStatus{
			{Name: "b1", BaseURL: "http://b1:8080", Freshness: scrape.FreshnessOK, LastSuccessAt: now},
		},
		SourceTuples: []types.SourceTuple{
			{
				BrokerName: "b1",
				BaseURL:    "http://b1:8080",
				WorkerURL:  "https://b1.example/",
				Offering: types.BrokerOffering{
					CapabilityID:    "openai:chat-completions:llama-3-70b",
					OfferingID:      "vllm-h100-batch4",
					InteractionMode: "http-stream@v1",
					WorkUnit:        types.WorkUnit{Name: "tokens"},
					PricePerUnitWei: "1500000",
					Extra:           map[string]any{"region": "us-west-2"},
				},
				ScrapedAt: now,
			},
		},
	}
}

func mustTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func TestBuild_Idempotent(t *testing.T) {
	snap := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
		PublicationSeq: 7,
	}
	a, err := Build(snap, opts)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Build(snap, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.ManifestBytes, b.ManifestBytes) {
		t.Fatalf("not idempotent:\n a=%s\n b=%s", a.ManifestBytes, b.ManifestBytes)
	}
}

func TestBuild_IssuedAtIsScrapeWindowEnd(t *testing.T) {
	snap := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	}
	c, err := Build(snap, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Manifest.IssuedAt.Equal(snap.WindowEnd) {
		t.Fatalf("issued_at = %s, want window end %s", c.Manifest.IssuedAt, snap.WindowEnd)
	}
}

func TestAggregate_PriceConflictHardFails(t *testing.T) {
	snap := sampleSnap()
	snap.SourceTuples = append(snap.SourceTuples, types.SourceTuple{
		BrokerName: "b2",
		WorkerURL:  "https://b2.example/",
		Offering: types.BrokerOffering{
			CapabilityID:    "openai:chat-completions:llama-3-70b",
			OfferingID:      "vllm-h100-batch4",
			InteractionMode: "http-stream@v1",
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1500001", // different price
			Extra:           map[string]any{"region": "us-west-2"},
		},
	})
	_, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	var conflict *PriceConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected PriceConflictError, got %v", err)
	}
}

func TestAggregate_HAPairDedupsToLexMin(t *testing.T) {
	snap := sampleSnap()
	snap.SourceTuples = append(snap.SourceTuples, types.SourceTuple{
		BrokerName: "b2",
		WorkerURL:  "https://aaa.example/",
		Offering: types.BrokerOffering{
			CapabilityID:    "openai:chat-completions:llama-3-70b",
			OfferingID:      "vllm-h100-batch4",
			InteractionMode: "http-stream@v1",
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1500000", // same price
			Extra:           map[string]any{"region": "us-west-2"},
		},
	})
	c, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Manifest.Capabilities) != 1 {
		t.Fatalf("expected 1 emitted tuple, got %d", len(c.Manifest.Capabilities))
	}
	if got := c.Manifest.Capabilities[0].WorkerURL; got != "https://aaa.example/" {
		t.Fatalf("expected lex-min worker_url, got %q", got)
	}
	if len(c.Metadata.HAEndpoints) != 1 {
		t.Fatalf("expected 1 HA sidecar entry, got %d", len(c.Metadata.HAEndpoints))
	}
}

func TestAggregate_DistinctExtraEmitsBoth(t *testing.T) {
	snap := sampleSnap()
	snap.SourceTuples = append(snap.SourceTuples, types.SourceTuple{
		BrokerName: "b2",
		WorkerURL:  "https://b2.example/",
		Offering: types.BrokerOffering{
			CapabilityID:    "openai:chat-completions:llama-3-70b",
			OfferingID:      "vllm-h100-batch4",
			InteractionMode: "http-stream@v1",
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1500000",
			Extra:           map[string]any{"region": "us-east-1"}, // distinct
		},
	})
	c, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Manifest.Capabilities) != 2 {
		t.Fatalf("expected 2 distinct tuples, got %d", len(c.Manifest.Capabilities))
	}
}

func TestCanonicalBytes_SortsKeys(t *testing.T) {
	v := map[string]any{"b": 1, "a": 2}
	out, err := CanonicalBytes(v)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"a":2,"b":1}` {
		t.Fatalf("got %s", out)
	}
}

func TestPackTarball_HasBothMembers(t *testing.T) {
	snap := sampleSnap()
	c, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	tarball, err := PackTarball(c)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	seen := map[string]int{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[hdr.Name] = len(body)
	}
	if seen["manifest.json"] == 0 {
		t.Fatal("manifest.json missing or empty")
	}
	if seen["metadata.json"] == 0 {
		t.Fatal("metadata.json missing or empty")
	}
}

func TestPackTarball_Idempotent(t *testing.T) {
	snap := sampleSnap()
	c1, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	a, err := PackTarball(c1)
	if err != nil {
		t.Fatal(err)
	}
	b, err := PackTarball(c2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("tarball not idempotent")
	}
}

func TestUniquenessKey_StableAcrossExtraOrder(t *testing.T) {
	a, _ := uniquenessKey(types.BrokerOffering{
		CapabilityID: "x", OfferingID: "y",
		Extra: map[string]any{"a": 1, "b": 2},
	})
	b, _ := uniquenessKey(types.BrokerOffering{
		CapabilityID: "x", OfferingID: "y",
		Extra: map[string]any{"b": 2, "a": 1},
	})
	if a != b {
		t.Fatalf("uniqueness key not stable across map order")
	}
	if !strings.Contains(a, "extra") {
		t.Fatalf("expected extra to appear in key, got %s", a)
	}
}
