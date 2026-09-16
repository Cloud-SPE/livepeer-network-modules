// Package ownership serves the shared GPU authority; it never accesses regional
// membership balances or payout records.
package ownership

import (
	"encoding/json"
	"net/http"

	api "github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func Handler(store *repo.OwnershipRepo, verifier serviceauth.Verifier) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /ownership/v1/devices/{device}", func(w http.ResponseWriter, r *http.Request) {
		poolID := r.Header.Get(serviceauth.PoolHeader)
		if _, err := verifier.Authorize(r, poolID, "ownership", "ownership-reader", "ownership-controller", "ownership-admin"); err != nil {
			http.Error(w, "unauthorized service", 401)
			return
		}
		record, err := store.Get(r.PathValue("device"))
		if err != nil {
			http.Error(w, "ownership unavailable", 503)
			return
		}
		if record.PoolID != poolID {
			record.MemberWallet = ""
			record.EnrollmentID = ""
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(record)
	})
	for _, action := range []string{"claim", "drain", "release", "fenced-recovery"} {
		mux.HandleFunc("POST /ownership/v1/"+action, func(w http.ResponseWriter, r *http.Request) {
			poolID := r.Header.Get(serviceauth.PoolHeader)
			roles := []string{"ownership-controller", "ownership-admin"}
			if action == "fenced-recovery" {
				roles = []string{"ownership-admin"}
			}
			principal, err := verifier.Authorize(r, poolID, "ownership", roles...)
			if err != nil {
				http.Error(w, "unauthorized service", 401)
				return
			}
			var req api.Request
			decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&req); err != nil || req.PoolID != poolID {
				http.Error(w, "invalid ownership request", 400)
				return
			}
			result, err := store.Change(action, principal.ID, req)
			if err != nil {
				http.Error(w, err.Error(), 409)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(result)
		})
	}
	return mux
}
