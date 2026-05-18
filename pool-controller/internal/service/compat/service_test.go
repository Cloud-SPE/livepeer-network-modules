package compat

import (
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestCheckMatchesClaim(t *testing.T) {
	offer := types.Offer{
		CapabilityID:    "rerank",
		OfferingID:      "zerank-2-default",
		InteractionMode: "http-reqresp@v0",
		WorkUnit:        config.WorkUnit{Name: "requests", Extractor: map[string]any{"type": "request-formula"}},
		Price:           config.Price{AmountWei: "1", PerUnits: 1},
	}
	backend := types.MemberBackend{
		Transport: "http",
		URL:       "http://backend",
		ClaimedCapabilities: []types.ClaimedOffer{{
			CapabilityID:    "rerank",
			OfferingID:      "zerank-2-default",
			InteractionMode: "http-reqresp@v0",
		}},
	}
	got := Check(offer, backend)
	if !got.Compatible {
		t.Fatalf("Check() = %#v, want compatible", got)
	}
	if got.MatchedClaim == nil || len(got.Checks) == 0 {
		t.Fatalf("Check() = %#v, want matched claim and checks", got)
	}
}

func TestCheckRejectsMismatch(t *testing.T) {
	offer := types.Offer{CapabilityID: "rerank", OfferingID: "zerank-2-default", InteractionMode: "http-reqresp@v0"}
	backend := types.MemberBackend{
		Transport: "http",
		URL:       "http://backend",
		ClaimedCapabilities: []types.ClaimedOffer{{
			CapabilityID:    "openai:chat-completions",
			InteractionMode: "http-stream@v0",
		}},
	}
	got := Check(offer, backend)
	if got.Compatible || len(got.Reasons) == 0 {
		t.Fatalf("Check() = %#v, want incompatibility", got)
	}
	if len(got.Checks) == 0 {
		t.Fatalf("Check() = %#v, want checks", got)
	}
}
