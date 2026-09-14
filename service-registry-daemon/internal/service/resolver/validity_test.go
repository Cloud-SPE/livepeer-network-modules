package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/service-registry-daemon/internal/types"
)

func resignPublication(t *testing.T, f *fixture, mutate func(*types.CoordinatorManifestPayload)) {
	t.Helper()
	var env types.CoordinatorSignedManifest
	if err := json.Unmarshal(f.fetcher.Bodies[f.uri], &env); err != nil {
		t.Fatal(err)
	}
	mutate(&env.Manifest)
	canonical, err := types.CoordinatorCanonicalBytes(env.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := f.signer.SignCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	env.Signature.Value = "0x" + hex(sig)
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	f.fetcher.Bodies[f.uri] = body
}

func TestPublicationWindow(t *testing.T) {
	for _, kind := range []string{"expired", "future", "inverted", "exact-expiry", "valid"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			publishOverlayFixture(t, f, "10")
			useManifestOverlay(t, f, f.uri)
			resignPublication(t, f, func(m *types.CoordinatorManifestPayload) {
				switch kind {
				case "expired":
					m.IssuedAt = f.clk.Now().Add(-2 * time.Hour)
					m.ExpiresAt = f.clk.Now().Add(-time.Hour)
				case "future":
					m.IssuedAt = f.clk.Now().Add(time.Minute)
				case "inverted":
					m.ExpiresAt = m.IssuedAt.Add(-time.Minute)
				case "exact-expiry":
					m.IssuedAt = f.clk.Now().Add(-time.Hour)
					m.ExpiresAt = f.clk.Now()
				}
			})
			_, err := f.svc.ResolveByAddress(context.Background(), Request{Address: f.addr})
			if kind == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, types.ErrManifestExpired) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestCachedPublicationCannotOutliveExpiry(t *testing.T) {
	for _, outage := range []string{"cached", "http", "chain"} {
		t.Run(outage, func(t *testing.T) {
			f := newFixture(t)
			publishOverlayFixture(t, f, "10")
			resignPublication(t, f, func(m *types.CoordinatorManifestPayload) { m.ExpiresAt = f.clk.Now().Add(time.Minute) })
			req := Request{Address: f.addr}
			if _, err := f.svc.ResolveByAddress(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			f.clk.T = f.clk.T.Add(time.Minute)
			if outage == "http" {
				delete(f.fetcher.Bodies, f.uri)
			}
			if outage == "chain" {
				f.chain.PreLoad(f.addr, "")
				f.svc.chain = &unavailableValidityChain{}
			}
			if _, err := f.svc.ResolveByAddress(context.Background(), req); !errors.Is(err, types.ErrManifestExpired) {
				t.Fatalf("expired cache returned: %v", err)
			}
		})
	}
}

type unavailableValidityChain struct{}

func (*unavailableValidityChain) GetServiceURI(context.Context, types.EthAddress) (string, error) {
	return "", types.ErrChainUnavailable
}

func TestPublicationReplayAndCanonicalRefetch(t *testing.T) {
	f := newFixture(t)
	publishOverlayFixture(t, f, "10")
	useManifestOverlay(t, f, f.uri)
	resignPublication(t, f, func(m *types.CoordinatorManifestPayload) { m.PublicationSeq = 5 })
	req := Request{Address: f.addr, ForceRefresh: true}
	if _, err := f.svc.ResolveByAddress(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	body := append([]byte(nil), f.fetcher.Bodies[f.uri]...)
	f.fetcher.Bodies[f.uri] = append([]byte("\n  "), body...)
	if _, err := f.svc.ResolveByAddress(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []uint64{4, 5} {
		f.fetcher.Bodies[f.uri] = body
		resignPublication(t, f, func(m *types.CoordinatorManifestPayload) {
			m.PublicationSeq = seq
			m.Capabilities[0].PricePerUnitWei = "20"
		})
		if _, err := f.svc.ResolveByAddress(context.Background(), req); !errors.Is(err, types.ErrParse) {
			t.Fatalf("accepted replay seq %d: %v", seq, err)
		}
	}
}
