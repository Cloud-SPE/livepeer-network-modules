package member

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
)

func registerDeviceTransferRoutes(mux *http.ServeMux, deps Deps) {
	mux.HandleFunc("GET /member/v1/device-transfers", func(w http.ResponseWriter, r *http.Request) {
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", 401)
			return
		}
		items, err := deps.Repo.DeviceTransfers()
		if err != nil {
			http.Error(w, err.Error(), 503)
			return
		}
		filtered := []memberTransferView{}
		for _, item := range items {
			if strings.EqualFold(item.MemberWallet, wallet) {
				filtered = append(filtered, transferView(item))
			}
		}
		writeJSON(w, 200, map[string]any{"pool_id": deps.Repo.PoolID(), "transfers": filtered})
	})
	mux.HandleFunc("POST /member/v1/hardware/{id}/transfer", func(w http.ResponseWriter, r *http.Request) {
		origin, err := url.Parse(r.Header.Get("Origin"))
		if err != nil || origin.Host != r.Host || (origin.Scheme != "https" && origin.Scheme != "http") {
			http.Error(w, "same-origin request required", 403)
			return
		}
		wallet, ok := memberIDFromRequest(deps.Sessions, r)
		if !ok {
			http.Error(w, "member authentication required", 401)
			return
		}
		if deps.BeginTransfer == nil {
			http.Error(w, "regional transfer unavailable", 503)
			return
		}
		var req struct {
			PoolID      string `json:"pool_id"`
			Destination string `json:"destination_pool_id"`
			Reason      string `json:"reason"`
		}
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&req); err != nil || req.PoolID != deps.Repo.PoolID() {
			http.Error(w, "invalid transfer request", 400)
			return
		}
		item, err := deps.BeginTransfer(r.Context(), r.PathValue("id"), wallet, req.Destination, req.Reason)
		if err != nil && item.ID == "" {
			http.Error(w, err.Error(), 409)
			return
		}
		status := http.StatusAccepted
		if item.Phase == "released" {
			status = http.StatusOK
		}
		writeJSON(w, status, transferView(item))
	})
}

// Members see their progress, never operator source URLs, credentials or raw
// infrastructure errors. Full pinned proofs remain on the admin audit surface.
type memberTransferView struct {
	ID                string    `json:"id"`
	PoolID            string    `json:"pool_id"`
	HardwareUnitID    string    `json:"hardware_unit_id"`
	EnrollmentID      string    `json:"enrollment_id"`
	DeviceID          string    `json:"device_id"`
	DestinationPoolID string    `json:"destination_pool_id"`
	Phase             string    `json:"phase"`
	Reason            string    `json:"reason"`
	LastError         string    `json:"last_error,omitempty"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func transferView(item repo.DeviceTransfer) memberTransferView {
	view := memberTransferView{ID: item.ID, PoolID: item.PoolID, HardwareUnitID: item.HardwareUnitID, EnrollmentID: item.EnrollmentID, DeviceID: item.DeviceID, DestinationPoolID: item.DestinationPoolID, Phase: item.Phase, Reason: item.Reason, UpdatedAt: item.UpdatedAt}
	if item.Phase != "released" {
		view.LastError = map[string]string{"requested": "Waiting for shared ownership and regional drain fences.", "draining": "Waiting for work and authorizations to settle on every regional source.", "revoking": "Waiting for credential revocation on every regional source.", "stopping": "Waiting for actual agent stop confirmation."}[item.Phase]
	}
	return view
}
