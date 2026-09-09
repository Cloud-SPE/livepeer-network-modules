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

// Retry policy for a debit that did not land.
//
// Bounded on purpose. An unbounded retry leaves a job that can never
// reach a terminal state, which is worse for a clearinghouse than a
// clear loss: an encumbrance it cannot release and cannot write off.
// When the bound is reached the exchange settles as DEBIT_FAILED, which
// is a recoverable outcome — somebody can act on it — rather than a
// permanent maybe.
const (
	debitRetryInterval    = 30 * time.Second
	debitRetryMaxAttempts = 10
	debitRetryMaxAge      = 30 * time.Minute
	debitRetryBatch       = 64
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

	// Exhaustion is checked BEFORE attempting, so a record that has run
	// out of budget settles on this pass instead of making one more call
	// nobody will look at.
	exhausted := pd.Attempts >= debitRetryMaxAttempts ||
		(!pd.FirstFailedAt.IsZero() && now.Sub(pd.FirstFailedAt) > debitRetryMaxAge)
	if pd.AccountAuthorization {
		// A stable-account reservation cannot be safely written off: releasing
		// it after delivery creates unpaid work; terminating it strands reusable
		// payer value. The receiver operation is idempotent, so reconcile until
		// an authoritative answer arrives.
		exhausted = false
	}
	if exhausted {
		s.settlePending(rec, pd.DebitedUnits, nil, true)
		log.Printf("ERROR: debit retry exhausted after %d attempts work_id=%s seq=%d units=%d "+
			"last_error=%q — settling DEBIT_FAILED; the exchange was delivered and is unpaid",
			pd.Attempts, pd.WorkID, pd.DebitSeq, pd.Units, pd.LastError)
		return
	}

	// The same debit_seq as the original attempt, deliberately. A debit
	// is idempotent by (sender, work_id, debit_seq), so if the first
	// attempt actually landed and only its response was lost, this
	// returns that debit rather than charging a second time.
	if pd.AccountAuthorization {
		ac, ok := s.payment.(payment.AccountClient)
		if !ok {
			_ = s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "wholesale account extension unavailable")
			return
		}
		settled, err := ac.SettleAuthorization(ctx, payment.SettleAuthorizationRequest{Payer: pd.Sender, AuthorizationID: pd.WorkID, ActualUnits: pd.ActualUnits, SettlementSeq: pd.DebitSeq})
		if err != nil {
			_ = s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), err.Error())
			return
		}
		if settled == nil || settled.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED) || settled.Account == nil {
			_ = s.sessionStore.RecordDebitRetryFailure(rec.RequestID, now.Add(debitRetryInterval), "payment daemon returned invalid authorization settlement state")
			return
		}
		s.settlePendingAccount(rec, settled)
		return
	}
	res, err := s.payment.DebitBalance(ctx, payment.DebitBalanceRequest{
		Sender:    pd.Sender,
		WorkID:    pd.WorkID,
		WorkUnits: int64(pd.Units),
		DebitSeq:  pd.DebitSeq,
	})
	if err != nil {
		next := now.Add(debitRetryInterval)
		if rerr := s.sessionStore.RecordDebitRetryFailure(rec.RequestID, next, err.Error()); rerr != nil {
			log.Printf("warning: recording debit retry failure failed request_id=%s: %v",
				rec.RequestID, rerr)
		}
		return
	}

	charged := res.DebitedWei
	if charged == nil {
		charged = big.NewInt(0)
	}
	s.settlePending(rec, pd.DebitedUnits+pd.Units, res, false)
	log.Printf("debit retry landed work_id=%s seq=%d units=%d charged=%s replayed=%v",
		pd.WorkID, pd.DebitSeq, pd.Units, charged, res.Replayed)
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

// settlePending builds the settlement the exchange should have had and
// moves the record terminal.
func (s *Server) settlePending(rec *sessionstore.JobRecord, debitedUnits uint64,
	res *payment.DebitResult, failed bool) {

	pd := rec.Pending
	funded, ok := new(big.Int).SetString(pd.FundedValueWei, 10)
	if !ok {
		funded = big.NewInt(0)
	}
	ident := middleware.SettlementIdentity{
		JobID:        pd.JobID,
		WorkID:       pd.WorkID,
		IssuedAt:     pd.IssuedAt,
		RequestID:    pd.RequestID,
		DebitFailed:  failed,
		DebitedUnits: debitedUnits,
	}
	if res != nil {
		ident.ChargedWei = res.DebitedWei
		ident.CumulativeUnits = res.CumulativeUnits
	}

	encoded := ""
	set := middleware.BuildSettlementRecord(middleware.SettlementInputs{
		PaymentBytes:   pd.PaymentBytes,
		FundedValueWei: funded,
		WorkUnit:       pd.WorkUnitName,
	}, pd.ActualUnits, pd.TerminationReason, ident)
	if set != nil {
		var err error
		encoded, err = settlement.Encode(set, s.settlementSigner)
		if err != nil {
			log.Printf("warning: settlement encode failed after debit retry request_id=%s: %v",
				pd.RequestID, err)
			encoded = ""
		}
	}
	if err := s.sessionStore.SettleJobWithUnits(rec.RequestID, debitedUnits, encoded); err != nil {
		log.Printf("warning: settling job after debit retry failed request_id=%s: %v",
			rec.RequestID, err)
	}
}
