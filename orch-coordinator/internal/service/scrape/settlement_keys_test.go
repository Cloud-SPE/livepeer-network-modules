package scrape

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify"
	specversion "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/version"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/providers/brokerclient"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

const skOrch = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func signedAnnouncement(t *testing.T, orch, baseURL string) (types.BrokerSettlementAnnouncement, string) {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pub := "0x" + hex.EncodeToString(crypto.FromECDSAPub(&key.PublicKey))
	st := types.BrokerSettlementStatement{OrchEthAddress: orch, PublicKey: pub, BaseURL: baseURL, IssuedAt: "2026-09-08T12:00:00Z"}
	canonical, _ := json.Marshal(struct {
		BaseURL        string `json:"base_url,omitempty"`
		IssuedAt       string `json:"issued_at"`
		OrchEthAddress string `json:"orch_eth_address"`
		PublicKey      string `json:"public_key"`
	}{st.BaseURL, st.IssuedAt, st.OrchEthAddress, st.PublicKey})
	sig, err := crypto.Sign(verify.PersonalSignDigest(canonical), key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	return types.BrokerSettlementAnnouncement{
		Statement: st,
		Signature: &types.BrokerSettlementSignature{Algorithm: "secp256k1", Canonicalization: "jcs", Value: "0x" + hex.EncodeToString(sig)},
	}, pub
}

func offeringsFor(orch string) *types.BrokerOfferings {
	return &types.BrokerOfferings{SpecVersion: specversion.VERSION, OrchEthAddress: orch, Capabilities: []types.BrokerOffering{{
		CapabilityID: "openai:chat-completions", OfferingID: "q", Protocol: "paid-job/v1",
		Job: &types.JobAxes{"transports": []any{"unary"}}, WorkUnit: types.WorkUnit{Name: "tokens"}, PricePerUnitWei: "1",
	}}}
}

func newKeyService(t *testing.T, fake *brokerclient.FakeClient, brokers ...config.Broker) *Service {
	t.Helper()
	svc, err := New(Config{
		OrchEthAddress: skOrch, Brokers: brokers,
		ScrapeInterval: time.Minute, ScrapeTimeout: time.Second, FreshnessWindow: time.Hour,
	}, fake, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// A proven announcement lands on the snapshot; one for another
// orchestrator is recorded, unproven, with the reason.
func TestScrape_SettlementKeysVerifiedPerBroker(t *testing.T) {
	good := config.Broker{Name: "good", BaseURL: "https://good.example"}
	bad := config.Broker{Name: "bad", BaseURL: "https://bad.example"}
	fake := brokerclient.NewFake()
	for _, b := range []config.Broker{good, bad} {
		fake.Set(b.BaseURL, offeringsFor(skOrch), nil)
	}
	ann, pub := signedAnnouncement(t, skOrch, good.BaseURL)
	fake.SetSettlementKeys(good.BaseURL, &types.BrokerSettlementKeys{SpecVersion: specversion.VERSION, OrchEthAddress: skOrch, Keys: []types.BrokerSettlementAnnouncement{ann}}, nil)
	foreign, _ := signedAnnouncement(t, "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", bad.BaseURL)
	fake.SetSettlementKeys(bad.BaseURL, &types.BrokerSettlementKeys{SpecVersion: specversion.VERSION, OrchEthAddress: skOrch, Keys: []types.BrokerSettlementAnnouncement{foreign}}, nil)

	svc := newKeyService(t, fake, good, bad)
	svc.ScrapeOnce(context.Background())
	snap := svc.Snapshot()
	byName := map[string]BrokerStatus{}
	for _, b := range snap.Brokers {
		byName[b.Name] = b
	}
	g := byName["good"]
	if !g.SettlementKeysSupported || g.SettlementKeysError != "" || len(g.SettlementKeys) != 1 || !g.SettlementKeys[0].Proven || g.SettlementKeys[0].PublicKey != pub {
		t.Fatalf("good: %+v", g)
	}
	if g.Freshness != FreshnessOK {
		t.Fatalf("settlement key handling touched freshness: %s", g.Freshness)
	}
	b := byName["bad"]
	if len(b.SettlementKeys) != 1 || b.SettlementKeys[0].Proven || b.SettlementKeys[0].Reason == "" {
		t.Fatalf("bad: %+v", b)
	}
}

// An older broker without the endpoint, and a broker whose endpoint
// fails, are both reported without disturbing the offerings scrape.
func TestScrape_SettlementKeysSoftFailures(t *testing.T) {
	old := config.Broker{Name: "old", BaseURL: "https://old.example"}
	down := config.Broker{Name: "down", BaseURL: "https://down.example"}
	fake := brokerclient.NewFake()
	fake.Set(old.BaseURL, offeringsFor(skOrch), nil)
	fake.Set(down.BaseURL, offeringsFor(skOrch), nil)
	fake.SetSettlementKeys(down.BaseURL, nil, errors.New("boom"))

	svc := newKeyService(t, fake, old, down)
	svc.ScrapeOnce(context.Background())
	for _, b := range svc.Snapshot().Brokers {
		if b.Freshness != FreshnessOK {
			t.Fatalf("%s: freshness %s", b.Name, b.Freshness)
		}
		switch b.Name {
		case "old":
			if b.SettlementKeysSupported || b.SettlementKeysError != "" || len(b.SettlementKeys) != 0 {
				t.Fatalf("old: %+v", b)
			}
		case "down":
			if b.SettlementKeysError == "" {
				t.Fatalf("down: error not recorded: %+v", b)
			}
		}
	}
}

// The older tests' hand-rolled fake predates the endpoint, which is a
// case the service has to handle anyway.
func (f *fakeClient) FetchSettlementKeys(ctx context.Context, baseURL string) (*types.BrokerSettlementKeys, error) {
	return nil, brokerclient.ErrNotSupported
}
