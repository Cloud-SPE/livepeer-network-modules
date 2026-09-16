package brokerpush

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"testing"
)

func TestRegionalOfferPartitionPreservesHistory(t *testing.T) {
	catalog := []templates.Template{{ID: "transcode", Capability: "video", OfferingID: "abr"}, {ID: "audio", Capability: "audio", OfferingID: "speech"}, {ID: "llm", Capability: "chat", OfferingID: "model"}}
	offers := []brokeradmin.OfferPush{{Capability: "video", OfferingID: "abr"}, {Capability: "audio", OfferingID: "speech"}, {Capability: "chat", OfferingID: "model"}, {Capability: "chat", OfferingID: "historical", Disabled: true}}
	for i, tmpl := range catalog {
		out, err := FilterOffers(catalog, offers, []string{tmpl.ID})
		if err != nil || len(out) != 4 {
			t.Fatalf("%v %v", out, err)
		}
		for j := range out {
			if out[j].Disabled != (j != i) {
				t.Fatalf("broker %s offer %d wrong enabled state", tmpl.ID, j)
			}
		}
	}
	if offers[0].Disabled {
		t.Fatal("mutated shared offers")
	}
	for _, ids := range [][]string{{"missing"}, {"audio", "audio"}} {
		if _, err := FilterOffers(catalog, offers, ids); err == nil {
			t.Fatal("accepted invalid selector")
		}
	}
	out, _ := FilterOffers(catalog, offers, []string{})
	for _, offer := range out {
		if !offer.Disabled {
			t.Fatal("empty selector enabled offer")
		}
	}
	out, _ = FilterOffers(catalog, offers, nil)
	if out[0].Disabled {
		t.Fatal("legacy changed")
	}
}
