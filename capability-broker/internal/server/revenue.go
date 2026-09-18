package server

import (
	"encoding/hex"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func (s *Server) handleRegionalRevenue(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil || cfg.ServiceAuthFile == "" || cfg.PoolID == "" {
		http.Error(w, "regional reporting unavailable", 404)
		return
	}
	if _, err := (serviceauth.Verifier{Path: cfg.ServiceAuthFile}).Authorize(r, cfg.PoolID, cfg.ServiceResource, "revenue-reader"); err != nil {
		http.Error(w, "unauthorized service", 401)
		return
	}
	round, err := strconv.ParseInt(r.PathValue("round"), 10, 64)
	if err != nil || round < 0 {
		http.Error(w, "invalid round", 400)
		return
	}
	reader, ok := s.payment.(payment.RevenueReader)
	if !ok {
		http.Error(w, "receiver reporting unavailable", 503)
		return
	}
	report, err := reader.RoundRevenue(r.Context(), round)
	if err != nil {
		http.Error(w, "receiver report unavailable", 503)
		return
	}
	observed, _ := time.Parse(time.RFC3339Nano, report.ObservedAt)
	adminJSON(w, http.StatusOK, revenue.Report{PoolID: cfg.PoolID, SourceID: report.SettlementDomainId, BrokerID: cfg.ServiceResource, ChainID: report.ChainId, Payee: "0x" + hex.EncodeToString(report.Payee), Round: report.RoundId, RevenueWei: new(big.Int).SetBytes(report.ConfirmedRevenueWei).String(), TicketCount: report.ConfirmedTicketCount, Complete: report.Complete, IncompleteReason: report.IncompleteReason, ObservedHead: report.ObservedHead, ObservedHeadHash: "0x" + hex.EncodeToString(report.ObservedHeadHash), FinalizedBlock: report.FinalizedBlock, FinalizedBlockHash: "0x" + hex.EncodeToString(report.FinalizedBlockHash), CompleteThroughRound: report.CompleteThroughRound, ObservedAt: observed, InclusionDigest: hex.EncodeToString(report.InclusionDigest)})
}
