package brokerpush

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

type fakePusher struct {
	offers      []brokeradmin.OfferPush
	creds       []brokeradmin.CredentialPush
	offerRev    string
	credRev     string
	offerResult *brokeradmin.PushResult
	credResult  *brokeradmin.PushResult
	err         error
}

func (f *fakePusher) PutOffers(_ context.Context, rev string, o []brokeradmin.OfferPush) (*brokeradmin.PushResult, error) {
	f.offers, f.offerRev = o, rev
	return f.offerResult, f.err
}

func (f *fakePusher) PutCredentials(_ context.Context, rev string, c []brokeradmin.CredentialPush) (*brokeradmin.PushResult, error) {
	f.creds, f.credRev = c, rev
	return f.credResult, nil
}

func sampleOffer() types.Offer {
	return types.Offer{
		ID: "off-1", CapabilityID: "openai:chat-completions", OfferingID: "llama-shared",
		Protocol: "paid-job/v1",
		Price:    config.Price{AmountWei: "210000000", PerUnits: 1},
		Extra: map[string]any{
			"openai":    map[string]any{"model": "llama-3-70b"},
			"region":    "us-west-2",
			"gpu_class": "h100",
		},
		Status: types.OfferStatusActive,
	}
}

// The push carries what the operator owns and nothing the runner
// declares — that separation is the whole epic.
func TestBuildOffersCarriesNoRunnerFacts(t *testing.T) {
	got := BuildOffers([]types.Offer{sampleOffer()})
	if len(got) != 1 {
		t.Fatalf("offers = %d", len(got))
	}
	raw, _ := json.Marshal(got[0])
	// Check the offer's own keys, not a substring of the whole payload:
	// "readiness" legitimately appears as a certification STEP TYPE (the
	// offer says run a readiness step; the runner declares the recipe).
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"offering_id": true, "capability": true, "protocol": true, "match": true, "price": true,
		"capacity": true, "extra": true, "constraints": true, "extra_from_runner": true,
		"certification": true, "disabled": true,
	}
	for k := range fields {
		if !allowed[k] {
			t.Fatalf("push carries %q, which is not part of the offer grammar: %s", k, raw)
		}
	}
	for _, runnerFact := range []string{"backend", "transports", "work_unit", "worker_url", "paths"} {
		if _, present := fields[runnerFact]; present {
			t.Fatalf("push carries the runner fact %q: %s", runnerFact, raw)
		}
	}
	if got[0].Price.AmountWei != "210000000" || got[0].Capability != "openai:chat-completions" {
		t.Fatalf("push = %s", raw)
	}
	// The selector is derived from the identity the operator declared.
	if got[0].Match["identity.openai.model"] != "llama-3-70b" {
		t.Fatalf("match = %v", got[0].Match)
	}
	if got[0].Extra["region"] != "us-west-2" {
		t.Fatalf("operator extra dropped: %v", got[0].Extra)
	}
}

func TestBuildOffersDefaultsAndDisabled(t *testing.T) {
	o := sampleOffer()
	o.Price.PerUnits = 0
	o.Status = types.OfferStatus("paused")
	o.Extra = nil
	got := BuildOffers([]types.Offer{o})
	if got[0].Price.PerUnits != 1 {
		t.Fatalf("per_units = %d, want the documented default", got[0].Price.PerUnits)
	}
	if !got[0].Disabled {
		t.Fatal("a non-active offer must be pushed disabled, not omitted")
	}
	if got[0].Match != nil {
		t.Fatalf("an offer naming no identity must match every runner of its capability: %v", got[0].Match)
	}
}

// Only the hash travels; a revoked or dropped enrollment is a revoke.
func TestBuildCredentialsPushesHashesOnly(t *testing.T) {
	now := time.Now().UTC()
	creds := BuildCredentials([]types.HostEnrollment{
		{ID: "host-1", MemberEthAddress: "0xabc", HostLabel: "rig", BrokerSessionCredential: "plaintext-secret",
			Status: types.HostEnrollmentActive, CreatedAt: now},
		{ID: "host-2", BrokerSessionCredential: "another", Status: types.HostEnrollmentRevoked, CreatedAt: now},
		{ID: "host-3", Status: types.HostEnrollmentActive, CreatedAt: now}, // no credential yet
	})
	if len(creds) != 2 {
		t.Fatalf("credentials = %d (a host with no credential must be skipped, not pushed empty)", len(creds))
	}
	raw, _ := json.Marshal(creds)
	if strings.Contains(string(raw), "plaintext-secret") || strings.Contains(string(raw), "another") {
		t.Fatalf("plaintext credential left the controller: %s", raw)
	}
	if creds[0].TokenSHA256 == "" || len(creds[0].TokenSHA256) != 64 {
		t.Fatalf("hash = %q", creds[0].TokenSHA256)
	}
	if creds[0].HostID != "host-1" || creds[0].MemberEthAddress != "0xabc" {
		t.Fatalf("credential = %+v", creds[0])
	}
	if creds[1].State != "revoked" {
		t.Fatalf("revoked enrollment pushed as %q", creds[1].State)
	}
}

// Offers go first, so a host whose credential was just accepted attaches
// into a broker that already knows what it might serve.
func TestSyncPushesOffersBeforeCredentials(t *testing.T) {
	f := &fakePusher{
		offerResult: &brokeradmin.PushResult{Changed: []string{"llama-shared"}},
		credResult:  &brokeradmin.PushResult{RevokedHosts: []string{"host-9"}},
	}
	res, err := Sync(context.Background(), f, State{
		Offers:      []types.Offer{sampleOffer()},
		Enrollments: []types.HostEnrollment{{ID: "host-1", BrokerSessionCredential: "s", Status: types.HostEnrollmentActive}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Offers != 1 || res.Credentials != 1 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.OffersChanged) != 1 || len(res.RevokedHosts) != 1 {
		t.Fatalf("result did not report what changed: %+v", res)
	}
	if f.offerRev == "" || f.offerRev == f.credRev {
		t.Fatalf("revisions = %q / %q; each set gets its own", f.offerRev, f.credRev)
	}
	// Identical state produces an identical revision, which is what
	// makes "nothing changed" observable.
	if again := Revision(BuildOffers([]types.Offer{sampleOffer()})); again != f.offerRev {
		t.Fatalf("revision is not stable: %q vs %q", again, f.offerRev)
	}
}

// Every family the controller used to probe in Go is now pushed as step
// config, and it must satisfy the protocol's schema. `make
// check-certification-policy` validates these goldens against it.
func TestCertificationPolicyGoldens(t *testing.T) {
	capabilities := []string{
		"openai:chat-completions", "openai:embeddings", "openai:audio-transcriptions",
		"openai:audio-speech", "openai:images-generations", "video:transcode.abr", "unknown:capability",
	}
	for _, capID := range capabilities {
		steps := CertificationPolicy(capID, "llama-3-70b")
		if len(steps) == 0 {
			t.Fatalf("%s: no steps", capID)
		}
		if steps[0].Type != "readiness" {
			t.Fatalf("%s: first step is %q, want readiness", capID, steps[0].Type)
		}
		// A usage step needs a preceding request step; the schema does
		// not encode that ordering, so assert it here.
		sawRequest := false
		for _, s := range steps {
			if s.Type == "request" {
				sawRequest = true
			}
			if (s.Type == "usage" || s.Type == "latency") && !sawRequest {
				t.Fatalf("%s: %s step has no preceding request", capID, s.Type)
			}
		}
		name := strings.NewReplacer(":", "-", ".", "-").Replace(capID) + ".json"
		got, err := json.MarshalIndent(steps, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, '\n')
		path := filepath.Join("..", "..", "..", "testdata", "certification", name)
		if os.Getenv("UPDATE_GOLDEN") == "1" {
			if err := os.WriteFile(path, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run UPDATE_GOLDEN=1 go test ./... to write)", name, err)
		}
		if string(got) != string(want) {
			t.Fatalf("%s drifted:\n got: %s\nwant: %s", name, got, want)
		}
	}
}

// Multipart families must not claim a JSON-shaped extractor's evidence:
// this is the heuristic the old probes.go got wrong.
func TestAudioPolicyIsMultipart(t *testing.T) {
	steps := CertificationPolicy("openai:audio-transcriptions", "whisper")
	var smoke *brokeradmin.OfferPushCertStep
	for i := range steps {
		if steps[i].Name == "smoke" {
			smoke = &steps[i]
		}
	}
	if smoke == nil || smoke.Config["transport"] != "multipart" {
		t.Fatalf("audio smoke step = %+v", smoke)
	}
	if _, hasBody := smoke.Config["body"]; hasBody {
		t.Fatal("a multipart step must not carry a JSON body")
	}
}
