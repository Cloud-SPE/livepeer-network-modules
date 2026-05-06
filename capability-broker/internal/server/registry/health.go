package registry

import (
	"encoding/json"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// HealthHandler returns the broker's currently-available capabilities. Per
// livepeer-network-protocol/headers/livepeer-headers.md, this is an unsigned
// fast-changing signal that gateway resolvers poll every 15-30s. The
// signed manifest is the slow-changing menu; this endpoint is the live view.
//
// v0.1 scaffold: returns "available" for every configured capability without
// probing the backend. Real backend health probes land in plan 0003's polish
// commit.
func HealthHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		statuses := make(map[string]string, len(cfg.Capabilities))
		for _, c := range cfg.Capabilities {
			statuses[c.ID] = "available"
		}
		statusesJSON, _ := json.Marshal(statuses)
		w.Header().Set(livepeerheader.HealthStatus, string(statusesJSON))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"capabilities": statuses,
		})
	}
}
