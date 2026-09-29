package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func (b *boltJobIdem) UpdateJob(id string, fn func(*sessionstore.JobRecord) error) error {
	return b.store.UpdateJob(id, fn)
}
func (m *memJobIdem) UpdateJob(id string, fn func(*sessionstore.JobRecord) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := m.recs[id]
	if r == nil {
		return sessionstore.ErrNotFound
	}
	cp := *r
	if err := fn(&cp); err != nil {
		return err
	}
	m.recs[id] = &cp
	return nil
}

type jobLifecycle struct {
	server              *Server
	id                  string
	prepared, uncertain bool
	selected            *config.Capability
	releaseSlot         func()
}

func (l *jobLifecycle) release() {
	if l.releaseSlot != nil {
		l.releaseSlot()
		l.releaseSlot = nil
	}
}
func (l *jobLifecycle) Prepare(pd *middleware.PendingDebit, funding []byte) error {
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(pd.AuthorizationBytes, &auth); err != nil {
		return err
	}
	p := auth.GetPayload()
	group, ok := l.server.groupFor(p.GetCapability(), p.GetOffering())
	if !ok {
		return middleware.ErrJobCapacity
	}
	c, release, err := l.server.reserveJobBackend(group)
	l.prepared = true
	if err != nil {
		if e := l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
			r.State = sessionstore.JobPaymentRejected
			r.Status = 503
			r.RejectedAuthorization = bytes.Clone(pd.AuthorizationBytes)
			r.BodyDigest = bytes.Clone(p.GetRequestDigest())
			r.EndedAt = time.Now().UTC()
			return nil
		}); e != nil {
			return e
		}
		return middleware.ErrJobCapacity
	}
	l.selected, l.releaseSlot = c, release
	return l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
		if r.State != sessionstore.JobInFlight {
			return sessionstore.ErrExists
		}
		r.Recovery = &sessionstore.JobRecovery{Admission: *toStorePending(pd), PaymentBytes: bytes.Clone(funding)}
		r.BodyDigest = bytes.Clone(p.GetRequestDigest())
		return nil
	})
}
func (l *jobLifecycle) Admitted(a *payment.AdmitAuthorizationResult) error {
	return l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
		if r.Recovery == nil {
			return sessionstore.ErrNotFound
		}
		cp := *r.Recovery
		cp.AdmissionRecorded = true
		cp.Admission.ReservedValueWei = decimalString(a.Reserved)
		cp.Admission.AccountFundingWei = decimalString(a.Credited)
		cp.Admission.AccountVersion = a.Account.Version
		r.Recovery = &cp
		return nil
	})
}
func (l *jobLifecycle) Executing() error {
	err := l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
		if r.Recovery == nil {
			return sessionstore.ErrNotFound
		}
		cp := *r.Recovery
		cp.ExecutionStarted = true
		r.Recovery = &cp
		return nil
	})
	if err != nil {
		l.uncertain = true
	}
	return err
}
func (l *jobLifecycle) Uncertain() { l.uncertain = true }
func (l *jobLifecycle) Settlement(pd *middleware.PendingDebit, status int) error {
	if l.uncertain {
		return fmt.Errorf("execution outcome unresolved")
	}
	return l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
		if r.Recovery == nil {
			return sessionstore.ErrNotFound
		}
		if status == 0 {
			status = 200
		}
		cp := *r.Recovery
		cp.UsageRecorded = true
		if cp.IssuedAt == "" {
			cp.IssuedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		r.Recovery = &cp
		r.Pending = toStorePending(pd)
		r.State = sessionstore.JobAccountingPending
		r.Status = status
		r.WorkUnits = pd.MeasuredUnits
		r.Unit = pd.WorkUnitName
		return nil
	})
}
func (l *jobLifecycle) Complete(record *pb.SettlementRecord) (string, error) {
	return l.server.completeJobSettlement(l.id, record)
}
func (s *Server) completeJobSettlement(id string, record *pb.SettlementRecord) (string, error) {
	var encoded string
	err := s.jobIdem.UpdateJob(id, func(r *sessionstore.JobRecord) error {
		if r.State == sessionstore.JobTerminal && r.Settlement != "" {
			encoded = r.Settlement
			return nil
		}
		if r.Recovery != nil {
			record.IssuedAt = r.Recovery.IssuedAt
		}
		record.SettlementSeq = 1
		var err error
		encoded, err = settlement.Encode(record, s.settlementSigner)
		if err != nil {
			return err
		}
		r.WorkUnits = record.GetActualUnits()
		return sessionstore.FinishRecoveredJob(r, encoded)
	})
	return encoded, err
}
func (l *jobLifecycle) Refused(ctx context.Context, cause error) error {
	// Even definitive refusal may race a previous lost admission. Receiver fencing
	// establishes the final result; an error code alone never frees authority.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := l.server.jobIdem.UpdateJob(l.id, func(r *sessionstore.JobRecord) error {
		r.AdmissionFailureReason = payment.SafeAdmissionReason(cause)
		return nil
	}); err != nil {
		return err
	}
	rec, err := l.server.jobIdem.ByRequestID(l.id)
	if err != nil {
		return err
	}
	if err := l.server.recoverJobAdmission(ctx, rec); err != nil {
		return err
	}
	fresh, err := l.server.jobIdem.ByRequestID(l.id)
	if err == nil && fresh.State == sessionstore.JobPaymentRejected {
		return middleware.ErrJobNotAdmitted
	}
	return err
}
func (s *Server) recoverJobAdmission(ctx context.Context, rec *sessionstore.JobRecord) error {
	if rec.Recovery == nil {
		return fmt.Errorf("job recovery intent missing")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	intent := rec.Recovery
	if intent.ExecutionStarted {
		return fmt.Errorf("runner execution requires reconciliation")
	}
	recoverer, ok := s.payment.(payment.AdmissionRecovery)
	if !ok {
		return errors.ErrUnsupported
	}
	result, err := recoverer.CancelAuthorizationAdmission(ctx, intent.Admission.AuthorizationBytes)
	if err != nil {
		return err
	}
	if result == nil {
		return fmt.Errorf("receiver recovery result missing")
	}
	var auth pb.SpendAuthorization
	if err := proto.Unmarshal(intent.Admission.AuthorizationBytes, &auth); err != nil {
		return err
	}
	p := auth.GetPayload()
	account := result.Account
	if account == nil || account.WholesaleAccountID != p.GetWholesaleAccountId() || account.SettlementDomainID != p.GetSettlementDomainId() || !bytes.Equal(account.Payer, p.GetPayer()) || !bytes.Equal(account.Payee, p.GetPayee()) {
		return fmt.Errorf("receiver recovery identity mismatch")
	}
	if result.Canceled || result.Authorization != nil && result.Authorization.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_EXPIRED_UNUSED) {
		return s.jobIdem.UpdateJob(rec.RequestID, func(r *sessionstore.JobRecord) error {
			if r.State != sessionstore.JobInFlight || r.Pending != nil {
				return sessionstore.ErrExists
			}
			r.State = sessionstore.JobPaymentRejected
			r.Status = http.StatusUnauthorized
			if r.AdmissionFailureReason == "INSUFFICIENT_WHOLESALE_CREDIT" {
				r.Status = http.StatusPaymentRequired
			}
			r.RejectedAuthorization = bytes.Clone(intent.Admission.AuthorizationBytes)
			r.EndedAt = time.Now().UTC()
			return nil
		})
	}
	a := result.Authorization
	if a == nil || result.Account == nil || a.ActualUnits != 0 || a.Billed == nil || a.Billed.Sign() != 0 ||
		(a.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) && a.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED)) {
		return fmt.Errorf("receiver contradicts unexecuted job")
	}
	pd := intent.Admission
	pd.ActualUnits = 0
	pd.MeasuredUnits = 0
	// Preserve the original reservation even if the receiver has already released it.
	if a.State == int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) {
		pd.ReservedValueWei = decimalString(a.Reserved)
	}
	pd.AccountVersion = result.Account.Version
	// Only a read-only receipt lookup may recover lost funding metadata. An
	// accepted authorization might have predated this optional funding request.
	if len(intent.PaymentBytes) > 0 && !intent.AdmissionRecorded {
		ac, ok := s.payment.(payment.FundingRecovery)
		if !ok {
			return errors.ErrUnsupported
		}
		receipt, err := ac.GetWholesaleFundingReceipt(ctx, intent.PaymentBytes, pd.WholesaleAccountID)
		if err != nil {
			return err
		}
		if receipt == nil {
			return fmt.Errorf("funding receipt missing")
		}
		pd.AccountFundingWei = decimalString(receipt.Credited)
	}
	if err := s.jobIdem.UpdateJob(rec.RequestID, func(r *sessionstore.JobRecord) error {
		if r.State != sessionstore.JobInFlight || r.Pending != nil {
			return sessionstore.ErrExists
		}
		cp := *r.Recovery
		cp.UsageRecorded = true
		if cp.IssuedAt == "" {
			cp.IssuedAt = time.Now().UTC().Format(time.RFC3339Nano)
		}
		r.Recovery = &cp
		r.Pending = &pd
		r.State = sessionstore.JobAccountingPending
		r.Status = 503
		r.Unit = pd.WorkUnitName
		return nil
	}); err != nil {
		return err
	}
	fresh, err := s.jobIdem.ByRequestID(rec.RequestID)
	if err != nil {
		return err
	}
	s.retryOneDebit(ctx, fresh, time.Now().UTC())
	return nil
}
