package registry

import (
	"encoding/json"
	"net/http"

	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/health"
	"github.com/Cloud-SPE/livepeer-network-rewrite/capability-broker/internal/livepeerheader"
)

// HealthHandler returns the broker's normalized live-health snapshot.
func HealthHandler(mgr *health.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snap := mgr.Snapshot()
		statuses := make(map[string]string, len(snap.Capabilities))
		for _, cap := range snap.Capabilities {
			statuses[cap.ID+"|"+cap.OfferingID] = string(cap.Status)
		}
		statusesJSON, _ := json.Marshal(statuses)
		w.Header().Set(livepeerheader.HealthStatus, string(statusesJSON))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(snap)
	}
}
