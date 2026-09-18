package brokerpush

import (
	"fmt"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/service/brokeradmin"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
)

// FilterOffers preserves every adopted offer's frozen historical shape while
// enabling only the templates assigned to this broker. Nil preserves legacy
// all-template behavior; an explicit empty list disables everything.
func FilterOffers(catalog []templates.Template, offers []brokeradmin.OfferPush, ids []string) ([]brokeradmin.OfferPush, error) {
	out := append([]brokeradmin.OfferPush(nil), offers...)
	if ids == nil {
		return out, nil
	}
	byID := map[string]string{}
	for _, t := range catalog {
		byID[t.ID] = t.Capability + "\x00" + t.OfferingID
	}
	selected := map[string]bool{}
	for _, id := range ids {
		key, ok := byID[id]
		if !ok || selected[key] {
			return nil, fmt.Errorf("unknown or duplicate broker template %q", id)
		}
		selected[key] = true
	}
	for i := range out {
		out[i].Disabled = out[i].Disabled || !selected[out[i].Capability+"\x00"+out[i].OfferingID]
	}
	return out, nil
}
