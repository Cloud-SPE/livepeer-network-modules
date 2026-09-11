package types

import (
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/config"
)

// TemplateOverride is everything a pool decides for itself about one
// catalog template (plan 0044 §3.2).
//
// The catalog says what a workload IS — its hardware requirements, its
// certification recipe, its compose fragment. Those are reviewed in
// version control and are the same for every pool running this build.
// What a pool varies is commercial and local: whether it sells this at
// all, what it charges, and what extra metadata it advertises. Keeping
// the two apart is what makes "enable template + set price" the whole
// of the operator's gesture.
type TemplateOverride struct {
	TemplateID string `json:"template_id"`
	// Enabled false is an explicit refusal, distinct from having no
	// override at all — which is why the whole record exists rather
	// than a set of enabled ids.
	Enabled bool `json:"enabled"`
	// Price overrides the template's price_default. Nil means the pool
	// accepts the catalog's suggestion.
	Price *config.Price `json:"price,omitempty"`
	// Extra is merged over the template's extra, key by key.
	Extra     map[string]any `json:"extra,omitempty"`
	UpdatedAt time.Time      `json:"updated_at"`
	UpdatedBy string         `json:"updated_by,omitempty"`
}
