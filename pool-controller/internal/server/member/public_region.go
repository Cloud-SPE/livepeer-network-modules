package member

import (
	"net/http"
	"time"
)

// PublicRegion deliberately projects only configured descriptions, never member
// state, runner locations, credentials, private extra metadata or earnings.
func registerPublicRegion(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/public-region", func(w http.ResponseWriter, r *http.Request) {
		overrides, err := deps.Repo.ListTemplateOverrides()
		if err != nil {
			http.Error(w, "regional catalog unavailable", 503)
			return
		}
		enabled := map[string]bool{}
		for _, override := range overrides {
			enabled[override.TemplateID] = override.Enabled
		}
		type offering struct {
			TemplateID  string `json:"template_id"`
			Capability  string `json:"capability"`
			OfferingID  string `json:"offering_id"`
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		offerings := []offering{}
		if deps.Catalog != nil {
			for _, template := range deps.Catalog.All() {
				if enabled[template.ID] {
					offerings = append(offerings, offering{template.ID, template.Capability, template.OfferingID, template.DisplayName, template.Description})
				}
			}
		}
		writeJSON(w, 200, map[string]any{"pool_id": deps.Repo.PoolID(), "observed_at": time.Now().UTC(), "controller_status": "available", "offerings": offerings, "offering_status": "configured; live routing eligibility is determined by regional brokers"})
	})
}
