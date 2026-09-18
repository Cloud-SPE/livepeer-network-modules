package server

import (
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"net/http"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func (s *Server) handleDeviceDrain(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil || cfg.ServiceAuthFile == "" || s.workAccounting == nil || cfg.Ownership.URL == "" {
		http.Error(w, "regional device drain unavailable", 503)
		return
	}
	if _, err := (serviceauth.Verifier{Path: cfg.ServiceAuthFile}).Authorize(r, cfg.PoolID, cfg.ServiceResource, "controller", "pool-admin"); err != nil {
		http.Error(w, "unauthorized service", 401)
		return
	}
	var req ownership.DeviceDrainRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil || req.PoolID != cfg.PoolID || req.EnrollmentID == "" || req.DeviceID == "" || req.Generation == 0 {
		http.Error(w, "invalid device drain request", 400)
		return
	}
	if req.Action != "" && req.Action != "drain" && req.Action != "revoke" {
		http.Error(w, "expected drain or revoke", 400)
		return
	}
	req.DeviceID = strings.ToLower(req.DeviceID)
	// A persisted fence can still be inspected after its credential is revoked.
	draining, err := s.workAccounting.Store.DeviceDraining(req.EnrollmentID, req.DeviceID, req.Generation)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if !draining {
		if s.credentialStore == nil {
			http.Error(w, "device credentials unavailable", 409)
			return
		}
		record, err := s.credentialStore.ByHost(req.EnrollmentID)
		if err != nil || record.PoolID != cfg.PoolID || record.DeviceOwnership[req.DeviceID] != req.Generation {
			http.Error(w, "device grant mismatch", 409)
			return
		}
		if err := s.workAccounting.BeginDeviceDrain(req.EnrollmentID, req.DeviceID, req.Generation, req.Reason); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
	}
	proof, err := s.workAccounting.DeviceDrainStatus(r.Context(), req.EnrollmentID, req.DeviceID, req.Generation)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	if s.credentialStore == nil {
		http.Error(w, "device credentials unavailable", 409)
		return
	}
	if req.Action == "revoke" {
		if proof.ActiveAuthorizations+proof.PendingOperations+proof.UndeliveredReceipts+proof.UnqualifiedWork != 0 {
			http.Error(w, "device accounting is not quiescent", 409)
			return
		}
		if err := s.credentialStore.RevokeDevice(cfg.PoolID, req.EnrollmentID, req.DeviceID, req.Generation, "regional transfer: "+req.Reason); err != nil {
			http.Error(w, err.Error(), 409)
			return
		}
		s.pruneOwnershipChecks()
	}
	proof.Revoked, err = s.credentialStore.DeviceRevoked(cfg.PoolID, req.EnrollmentID, req.DeviceID, req.Generation)
	if err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	adminJSON(w, 200, proof)
}
