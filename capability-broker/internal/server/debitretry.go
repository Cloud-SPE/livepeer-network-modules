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
	now := time.Now().UTC()
	due, err := s.sessionStore.DuePendingDebits(now, debitRetryBatch)
	if err != nil {
		log.Printf("warning: pending debit scan failed: %v", err)
		return
	}
	for _, rec := range due {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.retryOneDebit(ctx, rec, now)
	}
}

func (s *Server) retryOneDebit(ctx context.Context, rec *sessionstore.JobRecord, now time.Time) {
	pd := rec.Pending
	if pd == nil {
		return
	}

	// Settlement is idempotent by authorization id and sequence. Never
	// write an uncertain authorization off: doing so either releases value
	// after delivered work or strands reusable payer credit.
	ac, ok := s.payment.(payment.AccountClient)
	if !ok {
		_ = s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "wholesale account extension unavailable")
		return
	}
	settled, err := ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: pd.Sender, AuthorizationID: pd.WorkID, ActualUnits: pd.ActualUnits, SettlementSeq: pd.DebitSeq})
	if err != nil {
		if rerr := s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), err.Error()); rerr != nil {
			log.Printf("warning: recording debit retry failure failed request_id=%s: %v",
				rec.RequestID, rerr)
		}
		return
	}
	if settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) || settled.Account == nil {
		_ = s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "payment daemon returned invalid authorization settlement state")
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
	encoded, err := settlement.Encode(set, s.settlementSigner)
	if err != nil {
		log.Printf("warning: encode account settlement request_id=%s: %v", rec.RequestID, err)
		return
	}
	if err := s.sessionStore.SettleJobWithUnits(rec.RequestID, pd.ActualUnits, encoded); err != nil {
		log.Printf("warning: persist account settlement request_id=%s: %v", rec.RequestID, err)
	}
}
