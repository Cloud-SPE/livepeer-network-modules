package server

import (
	"context"
	"log"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

const (
	debitRetryInterval = 30 * time.Second
	debitRetryBatch    = 64
)

// runDebitRetry drives outstanding debits to a terminal accounting
// state. Started only when the broker has both a durable job store and
// a payment client.
func (s *Server) runDebitRetry(ctx context.Context) {
	s.sweepPendingDebits(ctx)
	t := time.NewTicker(debitRetryInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.sweepPendingDebits(ctx)
		}
	}
}

func (s *Server) sweepPendingDebits(ctx context.Context) {
	s.jobRecoveryMu.Lock()
	defer s.jobRecoveryMu.Unlock()
	now := time.Now().UTC()
	recoverable, scanErr := s.sessionStore.RecoverableJobs()
	if scanErr != nil {
		log.Printf("warning: job recovery scan: %v", scanErr)
		return
	}
	for _, r := range recoverable {
		if _, active := s.activeJobs.Load(r.RequestID); active {
			continue
		}
		if err := s.recoverJobAdmission(ctx, r); err != nil {
			log.Printf("job recovery pending request_id=%s: %v", r.RequestID, err)
		}
	}

	due, err := s.sessionStore.DuePendingDebits(now, debitRetryBatch)
	if err != nil {
		log.Printf("warning: pending debit scan failed: %v", err)
		return
	}
	for _, rec := range due {
		if _, active := s.activeJobs.Load(rec.RequestID); active {
			continue
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.retryOneDebit(ctx, rec, now)
	}
}

func (s *Server) retryOneDebit(ctx context.Context, rec *sessionstore.JobRecord, now time.Time) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	pd := rec.Pending
	if pd == nil {
		return
	}

	// Settlement is idempotent by authorization id and sequence. Never
	// write an uncertain authorization off: doing so either releases value
	// after delivered work or strands reusable payer credit.
	ac, ok := s.payment.(payment.AccountClient)
	if !ok {
		_ = s.recordJobRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "wholesale account extension unavailable")
		return
	}
	settled, err := ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{WholesaleAccountID: pd.WholesaleAccountID, Payer: pd.Sender, AuthorizationID: pd.WorkID, ActualUnits: pd.ActualUnits, SettlementSeq: pd.DebitSeq})
	if err != nil {
		if rerr := s.recordJobRetryFailure(rec.RequestID, now.Add(debitRetryInterval), err.Error()); rerr != nil {
			log.Printf("warning: recording debit retry failure failed request_id=%s: %v",
				rec.RequestID, rerr)
		}
		return
	}
	if settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) || settled.Account == nil {
		_ = s.recordJobRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "payment daemon returned invalid authorization settlement state")
		return
	}
	s.settlePendingAccount(rec, settled)
	log.Printf("authorization settlement retry landed authorization_id=%s seq=%d units=%d",
		pd.WorkID, pd.DebitSeq, pd.ActualUnits)
}

func (s *Server) settlePendingAccount(rec *sessionstore.JobRecord, res *payment.SettleAuthorizationResult) {
	pd := rec.Pending
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(pd.AuthorizationBytes, &auth); err != nil || auth.GetPayload() == nil {
		log.Printf("warning: decode pending account authorization request_id=%s", rec.RequestID)
		return
	}
	reserved, ok := new(big.Int).SetString(pd.ReservedValueWei, 10)
	if !ok {
		reserved = new(big.Int)
	}
	funding, ok := new(big.Int).SetString(pd.AccountFundingWei, 10)
	if !ok {
		funding = new(big.Int)
	}
	version := pd.AccountVersion
	if res.Account != nil {
		version = res.Account.Version
	}
	measured := pd.MeasuredUnits
	if measured == 0 {
		measured = pd.ActualUnits
	}
	set := middleware.BuildAuthorizationSettlement(auth.GetPayload(), reserved, res.Billed, res.Released, funding, version, measured, pd.ActualUnits, pd.JobID)
	if rec.Recovery != nil {
		if _, err := s.completeJobSettlement(rec.RequestID, set); err != nil {
			log.Printf("persist job settlement request_id=%s: %v", rec.RequestID, err)
		}
		return
	}
	encoded, err := settlement.Encode(set, s.settlementSigner)
	if err != nil {
		log.Printf("warning: encode account settlement request_id=%s: %v", rec.RequestID, err)
		return
	}
	if err := s.sessionStore.SettleJobWithUnits(rec.RequestID, pd.ActualUnits, encoded); err != nil {
		log.Printf("warning: persist account settlement request_id=%s: %v", rec.RequestID, err)
	}
}

func (s *Server) recordJobRetryFailure(id string, next time.Time, cause string) error {
	return s.jobIdem.UpdateJob(id, func(r *sessionstore.JobRecord) error {
		if r.Pending == nil {
			return sessionstore.ErrNotFound
		}
		cp := *r.Pending
		cp.Attempts++
		cp.NextAttemptAt = next
		cp.LastError = cause
		if cp.FirstFailedAt.IsZero() {
			cp.FirstFailedAt = time.Now().UTC()
		}
		r.Pending = &cp
		return nil
	})
}
