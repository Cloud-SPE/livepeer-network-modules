package server

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func (s *Server) handleSourceDrain(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg.ServiceAuthFile == "" || s.workAccounting == nil {
		http.Error(w, "regional source unavailable", 503)
		return
	}
	if !s.requireAdminAuth(w, r) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil {
		http.Error(w, "invalid drain", 400)
		return
	}
	if err := s.workAccounting.BeginDrain(req.Reason); err != nil {
		http.Error(w, err.Error(), 409)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) handleSourceFreeze(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg.ServiceAuthFile == "" || s.workAccounting == nil {
		http.Error(w, "regional source unavailable", 503)
		return
	}
	if !s.requireAdminAuth(w, r) {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&req); err != nil || req.Reason == "" {
		http.Error(w, "freeze reason required", 400)
		return
	}
	state, err := s.workAccounting.Store.DrainStatus()
	if err != nil || !state.Draining || state.PendingOperations != 0 || state.UndeliveredReceipts != 0 {
		http.Error(w, "source work finalization or delivery remains", 409)
		return
	}
	control, ok := s.workAccounting.Reader.(payment.SourceControl)
	if !ok {
		http.Error(w, "receiver source control unavailable", 503)
		return
	}
	if _, err = control.FreezeRevenueSource(r.Context(), req.Reason); err != nil {
		http.Error(w, "receiver source cannot freeze: "+err.Error(), 409)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) handleSourceReport(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg.ServiceAuthFile == "" || s.workAccounting == nil {
		http.Error(w, "regional source unavailable", 503)
		return
	}
	if _, err := (serviceauth.Verifier{Path: cfg.ServiceAuthFile}).Authorize(r, cfg.PoolID, cfg.ServiceResource, "revenue-reader"); err != nil {
		http.Error(w, "unauthorized service", 401)
		return
	}
	state, err := s.workAccounting.Store.DrainStatus()
	if err != nil {
		http.Error(w, "work inventory unavailable", 503)
		return
	}
	control, ok := s.workAccounting.Reader.(payment.SourceControl)
	if !ok {
		http.Error(w, "receiver source control unavailable", 503)
		return
	}
	receiver, err := control.RevenueSourceStatus(r.Context())
	if err != nil || receiver == nil || receiver.SettlementDomainId != s.workAccounting.Store.SourceID {
		http.Error(w, "receiver source report unavailable", 503)
		return
	}
	observed, _ := time.Parse(time.RFC3339Nano, receiver.ObservedAt)
	frozen, _ := time.Parse(time.RFC3339Nano, receiver.FrozenAt)
	proof := revenue.DrainReport{PoolID: cfg.PoolID, SourceID: receiver.SettlementDomainId, BrokerID: cfg.ServiceResource, ChainID: receiver.ChainId, Payee: "0x" + hex.EncodeToString(receiver.Payee), Draining: state.Draining, Frozen: receiver.Frozen, FrozenAt: frozen, ActiveAuthorizations: receiver.ActiveAuthorizations, PendingRedemptions: receiver.PendingRedemptions, PendingOperations: state.PendingOperations, UndeliveredReceipts: state.UndeliveredReceipts, LastReceiptRound: state.LastReceiptRound, LastRedemptionRound: receiver.LastRedemptionRound, CompleteThroughRound: receiver.CompleteThroughRound, ObservedAt: observed, Complete: receiver.Complete && state.Draining && state.PendingOperations == 0 && state.UndeliveredReceipts == 0, IncompleteReason: receiver.IncompleteReason}
	if !proof.Complete && proof.IncompleteReason == "" {
		proof.IncompleteReason = fmt.Sprintf("work pending=%d undelivered=%d draining=%t", state.PendingOperations, state.UndeliveredReceipts, state.Draining)
	}
	adminJSON(w, 200, proof)
}
