package brokerpush

import (
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"testing"
)

func TestSharedTranscodeOffersDoNotInventLocationOrVendor(t *testing.T) {
	cat, err := templates.Load("../../../../templates")
	if err != nil {
		t.Fatal(err)
	}
	var overrides []types.TemplateOverride
	for _, id := range []string{"video-transcode-vod", "video-transcode-abr", "video-transcode-live"} {
		overrides = append(overrides, types.TemplateOverride{TemplateID: id, Enabled: true})
	}
	offers := BuildOffersFromCatalog(cat.All(), overrides)
	if len(offers) != 3 {
		t.Fatal(offers)
	}
	for _, offer := range offers {
		if _, ok := offer.Constraints["region"]; ok {
			t.Fatalf("unverified geography: %+v", offer)
		}
		if _, ok := offer.Constraints["gpu_vendor"]; ok {
			t.Fatalf("mixed vendor advertised as one: %+v", offer)
		}
		if offer.Constraints["tier"] != "standard" {
			t.Fatalf("lost actual tier: %+v", offer)
		}
	}
}
