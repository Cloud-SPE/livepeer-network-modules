package candidate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version"
	"io"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/service/scrape"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

func sampleSnap() scrape.Snapshot {
	now := mustTime("2026-05-06T12:00:00Z")
	return scrape.Snapshot{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WindowStart:    now.Add(-30 * time.Second),
		WindowEnd:      now,
		Brokers: []scrape.BrokerStatus{
			{
				Name:          "b1",
				BaseURL:       "http://b1:8080",
				Freshness:     scrape.FreshnessOK,
				LastSuccessAt: now,
				TupleHealth: map[string]types.BrokerHealthCapability{
					"openai:chat-completions|vllm-h100-batch4": {
						ID:         "openai:chat-completions",
						OfferingID: "vllm-h100-batch4",
						Status:     "ready",
					},
				},
			},
		},
		SourceTuples: []types.SourceTuple{
			{
				BrokerName: "b1",
				BaseURL:    "http://b1:8080",
				WorkerURL:  "https://b1.example/",
				Offering: types.BrokerOffering{
					CapabilityID:    "openai:chat-completions",
					OfferingID:      "vllm-h100-batch4",
					Protocol:        "paid-job/v1",
					Job:             &types.JobAxes{"transports": []any{"stream"}},
					WorkUnit:        types.WorkUnit{Name: "tokens"},
					PricePerUnitWei: "1500000",
					Extra: map[string]any{
						"region": "us-west-2",
						"openai": map[string]any{"model": "llama-3-70b"},
					},
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

func TestBuild_DebouncesIssuedAtWhenContentUnchanged(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
		PublicationSeq: 7,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c1.ContentHash == "" {
		t.Fatal("expected non-empty ContentHash on first build")
	}

	// Second scrape: window advances 60s; capabilities are unchanged.
	second := sampleSnap()
	second.WindowStart = first.WindowEnd
	second.WindowEnd = first.WindowEnd.Add(60 * time.Second)

	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Manifest.IssuedAt.Equal(c1.Manifest.IssuedAt) {
		t.Fatalf("issued_at drifted: c1=%s c2=%s", c1.Manifest.IssuedAt, c2.Manifest.IssuedAt)
	}
	if !c2.Manifest.ExpiresAt.Equal(c1.Manifest.ExpiresAt) {
		t.Fatalf("expires_at drifted: c1=%s c2=%s", c1.Manifest.ExpiresAt, c2.Manifest.ExpiresAt)
	}
	if !bytes.Equal(c1.ManifestBytes, c2.ManifestBytes) {
		t.Fatalf("manifest_bytes drifted on identical content")
	}
}

func TestBuild_AdvancesIssuedAtOnContentChange(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Second scrape: window advances AND price changes (content drift).
	second := sampleSnap()
	second.WindowStart = first.WindowEnd
	second.WindowEnd = first.WindowEnd.Add(60 * time.Second)
	second.SourceTuples[0].Offering.PricePerUnitWei = "9000000"

	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Manifest.IssuedAt.Equal(c1.Manifest.IssuedAt) {
		t.Fatalf("expected issued_at to advance on content change; got %s", c2.Manifest.IssuedAt)
	}
	if !c2.Manifest.IssuedAt.Equal(second.WindowEnd) {
		t.Fatalf("issued_at = %s; want window end %s", c2.Manifest.IssuedAt, second.WindowEnd)
	}
	if c2.ContentHash == c1.ContentHash {
		t.Fatal("expected ContentHash to differ on content change")
	}
}

func TestBuild_DebouncesIssuedAtWhenOnlyVolatileMetadataChanges(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Second scrape: the window advances and the tuple's live health
	// degrades, but the capability content (orch + capabilities tuples)
	// is unchanged. Neither is manifest content, so neither may move
	// issued_at — a signed manifest that re-issues on every scrape burns
	// a signature for nothing and looks like a change to a gateway.
	second := sampleSnap()
	second.WindowStart = first.WindowEnd
	second.WindowEnd = first.WindowEnd.Add(60 * time.Second)
	second.Brokers[0].TupleHealth["openai:chat-completions|vllm-h100-batch4"] = types.BrokerHealthCapability{
		ID:         "openai:chat-completions",
		OfferingID: "vllm-h100-batch4",
		Status:     "unreachable",
		Reason:     "no_eligible_runner",
	}

	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Manifest.IssuedAt.Equal(c1.Manifest.IssuedAt) {
		t.Fatalf("issued_at drifted on volatile-metadata-only change: c1=%s c2=%s", c1.Manifest.IssuedAt, c2.Manifest.IssuedAt)
	}
	if !bytes.Equal(c1.ManifestBytes, c2.ManifestBytes) {
		t.Fatal("manifest_bytes drifted on volatile-metadata-only change")
	}
	// The sidecar still moves: it is not signed and its whole job is to
	// report the scrape that just happened.
	if !c2.Metadata.ScrapeWindowEnd.After(c1.Metadata.ScrapeWindowEnd) {
		t.Fatal("sidecar did not advance its scrape window")
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
			CapabilityID:    "openai:chat-completions",
			OfferingID:      "vllm-h100-batch4",
			Protocol:        "paid-job/v1",
			Job:             &types.JobAxes{"transports": []any{"stream"}},
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1500001", // different price
			Extra: map[string]any{
				"region": "us-west-2",
				"openai": map[string]any{"model": "llama-3-70b"},
			},
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
			CapabilityID:    "openai:chat-completions",
			OfferingID:      "vllm-h100-batch4",
			Protocol:        "paid-job/v1",
			Job:             &types.JobAxes{"transports": []any{"stream"}},
			WorkUnit:        types.WorkUnit{Name: "tokens"},
			PricePerUnitWei: "1500000", // same price
			Extra: map[string]any{
				"region": "us-west-2",
				"openai": map[string]any{"model": "llama-3-70b"},
			},
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
			CapabilityID:    "openai:chat-completions",
			OfferingID:      "vllm-h100-batch4",
			Protocol:        "paid-job/v1",
			Job:             &types.JobAxes{"transports": []any{"stream"}},
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

func TestBuild_PreservesOpenAICapabilityIDAndModelExtra(t *testing.T) {
	snap := sampleSnap()
	c, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := c.Manifest.Capabilities[0]
	if got.CapabilityID != "openai:chat-completions" {
		t.Fatalf("capability_id = %q", got.CapabilityID)
	}
	openaiExtra, _ := got.Extra["openai"].(map[string]any)
	if openaiExtra["model"] != "llama-3-70b" {
		t.Fatalf("model extra = %#v", openaiExtra["model"])
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

func TestBuild_RenewalWindowRefreshesIssuedAtWhenContentUnchanged(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
		PublicationSeq: 7,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}

	// Window advances 17h: remaining validity (7h) is below the
	// default renewal threshold (TTL/3 = 8h), so the debounce must
	// yield to a fresh window even though content is unchanged.
	second := sampleSnap()
	second.WindowStart = first.WindowEnd.Add(17*time.Hour - 30*time.Second)
	second.WindowEnd = first.WindowEnd.Add(17 * time.Hour)

	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Manifest.IssuedAt.Equal(second.WindowEnd) {
		t.Fatalf("issued_at not refreshed in renewal window: got %s want %s", c2.Manifest.IssuedAt, second.WindowEnd)
	}
	if bytes.Equal(c1.ManifestBytes, c2.ManifestBytes) {
		t.Fatal("renewal must produce fresh signable bytes")
	}
	if c2.ContentHash != c1.ContentHash {
		t.Fatal("renewal must not change the content hash")
	}
}

func TestBuild_RenewalRefreshIsOneShot(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
		PublicationSeq: 7,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}

	second := sampleSnap()
	second.WindowStart = first.WindowEnd.Add(17*time.Hour - 30*time.Second)
	second.WindowEnd = first.WindowEnd.Add(17 * time.Hour)
	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}

	// The scrape after the renewal refresh debounces to the renewed
	// issued_at: candidate bytes stay stable while the sign cycle is
	// in flight, instead of churning every scrape.
	third := sampleSnap()
	third.WindowStart = second.WindowEnd
	third.WindowEnd = second.WindowEnd.Add(60 * time.Second)
	opts.PrevContentHash = c2.ContentHash
	opts.PrevIssuedAt = c2.Manifest.IssuedAt
	c3, err := Build(third, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c3.Manifest.IssuedAt.Equal(c2.Manifest.IssuedAt) {
		t.Fatalf("renewed issued_at must debounce again: c2=%s c3=%s", c2.Manifest.IssuedAt, c3.Manifest.IssuedAt)
	}
	if !bytes.Equal(c2.ManifestBytes, c3.ManifestBytes) {
		t.Fatal("manifest bytes churned after renewal refresh")
	}
}

func TestBuild_ExplicitRenewalThresholdKeepsDebounce(t *testing.T) {
	first := sampleSnap()
	opts := BuildOptions{
		OrchEthAddress:   "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:      24 * time.Hour,
		PublicationSeq:   7,
		RenewalThreshold: time.Hour,
	}
	c1, err := Build(first, opts)
	if err != nil {
		t.Fatal(err)
	}

	// 17h in, remaining validity is 7h — above the explicit 1h
	// threshold, so the debounce holds.
	second := sampleSnap()
	second.WindowStart = first.WindowEnd.Add(17*time.Hour - 30*time.Second)
	second.WindowEnd = first.WindowEnd.Add(17 * time.Hour)
	opts.PrevContentHash = c1.ContentHash
	opts.PrevIssuedAt = c1.Manifest.IssuedAt
	c2, err := Build(second, opts)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Manifest.IssuedAt.Equal(c1.Manifest.IssuedAt) {
		t.Fatalf("debounce should hold above explicit threshold: c1=%s c2=%s", c1.Manifest.IssuedAt, c2.Manifest.IssuedAt)
	}
}

// The signed bytes are the manifest. Spec 1.0.0 requires `protocol` plus
// exactly the axes object that matches it; anything the coordinator drops
// here is dropped from what the cold key signs, and the published manifest
// fails schema validation at every resolver.
func TestBuild_EmitsProtocolAndDeclaredAxesVerbatim(t *testing.T) {
	snap := sampleSnap()
	snap.SourceTuples = []types.SourceTuple{
		{
			BrokerName: "b1",
			WorkerURL:  "https://b1.example/",
			Offering: types.BrokerOffering{
				CapabilityID:    "openai:chat-completions",
				OfferingID:      "vllm",
				Protocol:        "paid-job/v1",
				Job:             &types.JobAxes{"transports": []any{"unary", "stream"}},
				WorkUnit:        types.WorkUnit{Name: "tokens"},
				PricePerUnitWei: "1500000",
			},
		},
		{
			BrokerName: "b2",
			WorkerURL:  "https://b2.example/",
			Offering: types.BrokerOffering{
				CapabilityID: "video:transcode.live",
				OfferingID:   "h264",
				Protocol:     "paid-session/v1",
				Session: &types.SessionAxes{
					"descriptor_schema":      "rtmp-hls/v1",
					"metering":               "runner-reported",
					"heartbeat":              map[string]any{"interval_seconds": float64(10), "missed_threshold": float64(3)},
					"runway_increment_units": float64(60000),
				},
				WorkUnit:        types.WorkUnit{Name: "video-frame-megapixel"},
				PricePerUnitWei: "200000",
			},
		},
	}

	c, err := Build(snap, BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		Capabilities []map[string]any `json:"capabilities"`
	}
	if err := json.Unmarshal(c.ManifestBytes, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Capabilities) != 2 {
		t.Fatalf("expected 2 tuples, got %d", len(got.Capabilities))
	}

	byCap := map[string]map[string]any{}
	for _, e := range got.Capabilities {
		byCap[e["capability_id"].(string)] = e
	}

	job := byCap["openai:chat-completions"]
	if job["protocol"] != "paid-job/v1" {
		t.Fatalf("protocol = %v", job["protocol"])
	}
	// additionalProperties:false — assert the exact key set, so any
	// stray leftover key (the pre-1.0.0 mode field included) fails here.
	assertKeys(t, job, "capability_id", "offering_id", "protocol", "job",
		"work_unit", "price_per_unit_wei", "worker_url")
	wantJob := map[string]any{"transports": []any{"unary", "stream"}}
	if !reflect.DeepEqual(job["job"], wantJob) {
		t.Fatalf("job axes = %#v, want %#v", job["job"], wantJob)
	}
	if _, bad := job["session"]; bad {
		t.Fatal("paid-job tuple must not carry a session object")
	}

	sess := byCap["video:transcode.live"]
	if sess["protocol"] != "paid-session/v1" {
		t.Fatalf("protocol = %v", sess["protocol"])
	}
	wantSess := map[string]any{
		"descriptor_schema":      "rtmp-hls/v1",
		"metering":               "runner-reported",
		"heartbeat":              map[string]any{"interval_seconds": float64(10), "missed_threshold": float64(3)},
		"runway_increment_units": float64(60000),
	}
	if !reflect.DeepEqual(sess["session"], wantSess) {
		t.Fatalf("session axes = %#v, want %#v", sess["session"], wantSess)
	}
	if _, bad := sess["job"]; bad {
		t.Fatal("paid-session tuple must not carry a job object")
	}
	assertKeys(t, sess, "capability_id", "offering_id", "protocol", "session",
		"work_unit", "price_per_unit_wei", "worker_url")
}

func assertKeys(t *testing.T, entry map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("emitted keys = %v, want %v", got, want)
	}
}

// The candidate publishes the sign policy it actually applies, so the
// console reads it instead of keeping its own copy (plan 0043 §3.7).
func TestBuild_MetadataPublishesEffectiveSignPolicy(t *testing.T) {
	base := BuildOptions{
		OrchEthAddress: "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ManifestTTL:    24 * time.Hour,
		PublicationSeq: 7,
	}
	// Unset threshold: the published value is the applied default
	// (ttl/3), never 0 — a reader must not have to re-derive it.
	c, err := Build(sampleSnap(), base)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Metadata.ManifestTTLSeconds; got != int64((24 * time.Hour).Seconds()) {
		t.Fatalf("manifest_ttl_seconds = %d", got)
	}
	if got := c.Metadata.RenewalThresholdSeconds; got != int64((8 * time.Hour).Seconds()) {
		t.Fatalf("renewal_threshold_seconds = %d, want the applied ttl/3 default", got)
	}

	// Explicit threshold is published verbatim.
	withThreshold := base
	withThreshold.RenewalThreshold = 90 * time.Minute
	c, err = Build(sampleSnap(), withThreshold)
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Metadata.RenewalThresholdSeconds; got != int64((90 * time.Minute).Seconds()) {
		t.Fatalf("renewal_threshold_seconds = %d, want 5400", got)
	}

	// The spec version has one source.
	if c.Manifest.SpecVersion != version.VERSION || c.Metadata.SchemaVersion != version.VERSION {
		t.Fatalf("spec version drifted from the protocol module: manifest=%q metadata=%q module=%q",
			c.Manifest.SpecVersion, c.Metadata.SchemaVersion, version.VERSION)
	}
}
