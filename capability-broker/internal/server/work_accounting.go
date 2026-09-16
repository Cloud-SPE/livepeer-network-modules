package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func (s *Server) initWorkAccounting() error {
	if s.cfg.PoolID == "" {
		return nil
	}
	account, ok := s.payment.(payment.AccountClient)
	if !ok {
		return fmt.Errorf("regional billing requires wholesale receiver")
	}
	domain, ok := s.payment.(payment.DomainClient)
	if !ok {
		return fmt.Errorf("regional billing requires receiver identity")
	}
	reader, ok := s.payment.(payment.RevenueReader)
	if !ok {
		return fmt.Errorf("regional billing requires receiver coverage")
	}
	if s.receiptSink == nil {
		return fmt.Errorf("regional billing requires durable receipt delivery endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source, err := domain.SettlementDomain(ctx)
	if err != nil {
		return err
	}
	store, err := workledger.Open(s.cfg.AccountingStorePath, s.cfg.PoolID, source, s.cfg.ServiceResource)
	if err != nil {
		return err
	}
	s.workAccounting = &workledger.Client{RequireTerms: true, Client: s.payment, AccountClient: account, Store: store, Reader: reader, Domain: domain, Sink: s.receiptSink}
	recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 30*time.Second)
	err = s.workAccounting.RecoverUnbound(recoveryCtx)
	recoveryCancel()
	if err != nil {
		_ = store.Close()
		s.workAccounting = nil
		return fmt.Errorf("recover unbound admissions: %w", err)
	}
	s.payment = s.workAccounting
	s.receiptSink = nil // Regional receipts are emitted once by the durable outbox.
	return nil
}
func (s *Server) bindAccountingWork(workID, requestID, capability, offering, backend string) error {
	if s.workAccounting == nil {
		return nil
	}
	host, local := splitBackendID(backend)
	if local == "" || s.credentialStore == nil {
		return fmt.Errorf("regional work requires enrolled runner")
	}
	record, err := s.credentialStore.ByHost(host)
	if err != nil {
		return err
	}
	if record.PoolID != s.cfg.PoolID || record.MemberEthAddress == "" || (record.State != credentialstore.StateActive && record.State != credentialstore.StateRotating) {
		return fmt.Errorf("regional member attribution unavailable")
	}
	if s.workAccounting.RequireTerms {
		if existing, err := s.workAccounting.Store.Binding(workID); err == nil {
			if existing.Member != record.MemberEthAddress || existing.Backend != backend || existing.Capability != capability || existing.Offering != offering {
				return fmt.Errorf("existing regional work binding mismatch")
			}
			return nil
		}
		if err := s.workAccounting.Store.TermsPermitAdmission(); err != nil {
			return err
		}
		policy, err := s.workAccounting.Store.TermsPolicy()
		if err != nil {
			return err
		}
		if record.TermsVersion == "" || record.TermsVersion != policy.Version {
			return fmt.Errorf("member has not accepted the active regional terms")
		}
	}
	devices := map[string]uint64{}
	if s.cfg.Ownership.URL != "" {
		snapshot, ok := s.runners.Get(host)
		if !ok {
			return fmt.Errorf("runner device attribution unavailable")
		}
		for _, view := range snapshot.Capabilities {
			cap := view.Capability
			if cap == nil || cap.LocalID != local {
				continue
			}
			for _, device := range cap.Devices {
				device = strings.ToLower(device)
				generation := record.DeviceOwnership[device]
				if generation == 0 {
					return fmt.Errorf("device grant unavailable")
				}
				devices[device] = generation
			}
		}
		if len(devices) == 0 {
			return fmt.Errorf("work has no device ownership attribution")
		}
	}
	return s.workAccounting.Store.Bind(workID, workledger.Attribution{DeviceOwnership: devices, TermsVersion: record.TermsVersion, Member: record.MemberEthAddress, Backend: backend, Enrollment: host, Capability: capability, Offering: offering, RequestID: requestID})
}
func (s *Server) runWorkAccounting(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		attempt, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.workAccounting.Flush(attempt)
		cancel()
		if err != nil && ctx.Err() == nil {
			log.Printf("regional work accounting held: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
func (s *Server) handleRegionalWork(w http.ResponseWriter, r *http.Request) {
	cfg := s.currentConfig()
	if cfg == nil || cfg.ServiceAuthFile == "" || s.workAccounting == nil {
		http.Error(w, "regional work reporting unavailable", 503)
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
	report, err := s.workAccounting.Store.Report(round, time.Now())
	if err != nil {
		http.Error(w, "work report unavailable", 503)
		return
	}
	adminJSON(w, 200, report)
}
