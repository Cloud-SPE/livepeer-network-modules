package sessionengine

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// Settlement for paid-session/v1.
//
// A session settlement is built from the broker's durable cumulative usage,
// authorization, account result, and quote binding. Ticket-funding generation
// state is deliberately absent: it funds the aggregate account and neither
// identifies nor authorizes this workload.
//
// The authoritative billing quantity is cumulative debited_units — what
// the ledger moved — not claimed_units, which is only what a runner
// asserted. Coarse runner reports can cross the signed allowance: excess
// claimed work is unbilled seller risk, never additional payer liability.

// SettlementFor builds the settlement record for a session as of now.
// State distinguishes an interim snapshot from a final settlement, so a
// reader can tell "this session is still running" from "this is what it
// cost".
func (e *Engine) SettlementFor(rec *sessionstore.Record, spec *OfferingSpec) *pb.SettlementRecord {
	if rec == nil {
		return nil
	}
	if len(rec.TerminalSettlement) > 0 {
		var frozen pb.SettlementRecord
		if proto.Unmarshal(rec.TerminalSettlement, &frozen) != nil {
			return nil
		}
		return &frozen
	}
	spec = acceptedSettlementSpec(rec, spec)
	if spec == nil {
		return nil
	}
	amount := spec.PricePerWorkUnitWei
	if amount == nil {
		amount = new(big.Int)
	}
	perUnits := spec.PerUnits
	if perUnits == 0 {
		perUnits = 1
	}

	// One ceiling over the cumulative total — never a sum of per-event
	// ceilings, which would reintroduce rounding drift.
	billed := payment.BillFor(amount, perUnits, rec.DebitedTotal)
	if v, ok := new(big.Int).SetString(rec.BilledWei, 10); ok && v != nil && v.Sign() > 0 {
		billed = v
	}
	out := &pb.SettlementRecord{
		WholesaleAccountId: rec.WholesaleAccountID, SettlementDomainId: rec.SettlementDomainID,
		AcceptedQuoteRef: &pb.QuoteRef{
			QuoteId:               rec.QuoteID,
			QuoteVersion:          rec.QuoteVersion,
			ConstraintFingerprint: append([]byte(nil), rec.ConstraintFingerprint...),
			RouteFingerprint:      append([]byte(nil), rec.RouteFingerprint...),
		},
		WorkUnitName:   rec.Unit,
		ActualUnits:    rec.DebitedTotal,
		BilledUnits:    rec.DebitedTotal,
		BilledValueWei: &pb.BigUInt{Value: billed.Bytes()},

		SessionId: rec.SessionID,
		// The consumer's own identifier. session_id is broker-local and
		// work_id can be shared, so this is the only field that binds a
		// record to the session a clearinghouse issued.
		GatewaySessionId: rec.GatewaySessionID,
		WorkId:           rec.AccountAuthorizationID,

		ClaimedUnits:   rec.ClaimedTotal,
		DebitedUnits:   rec.DebitedTotal,
		FundedValueWei: &pb.BigUInt{Value: decimalBytes(rec.FundedWei)},

		AmountWei: &pb.BigUInt{Value: amount.Bytes()},
		PerUnits:  perUnits,

		SettlementSeq: rec.SettlementSeq,
		IssuedAt:      e.cfg.Now().UTC().Format(time.RFC3339Nano),
		State:         wireState(rec.State),
	}
	if rec.AccountAuthorizationID != "" {
		out.AuthorizationId = rec.AccountAuthorizationID
		out.AuthorizedValueWei = &pb.BigUInt{Value: decimalBytes(rec.AuthorizationMaxDebitWei)}
		out.ReservedValueWei = &pb.BigUInt{Value: decimalBytes(rec.AuthorizationReservedWei)}
		out.ReleasedValueWei = &pb.BigUInt{Value: decimalBytes(rec.AuthorizationReleasedWei)}
		out.AccountFundingValueWei = &pb.BigUInt{Value: decimalBytes(rec.FundedWei)}
	}
	breakdown := make(map[string]string, 4)
	if rec.CloseReason != "" {
		breakdown["termination_reason"] = rec.CloseReason
	}
	if rec.OutputState != "" {
		breakdown["output_state"] = rec.OutputState
	}
	if !rec.OutputStateSince.IsZero() {
		breakdown["output_state_since"] = rec.OutputStateSince.UTC().Format(time.RFC3339Nano)
	}
	if rec.LastFailureCode != "" {
		breakdown["last_failure_code"] = rec.LastFailureCode
	}
	if rec.ClaimedTotal != rec.DebitedTotal {
		// Preserve the observation; authorization exhaustion can intentionally
		// leave delivered work above the paid cap.
		breakdown["claim_debit_gap"] = "true"
		if rec.ClaimedTotal > rec.DebitedTotal && rec.DebitedTotal == rec.AuthorizationMaxUnits && rec.CloseReason == ReasonAuthorizationExhausted {
			breakdown["claim_debit_gap_reason"] = "authorization_cap"
		} else {
			breakdown["claim_debit_gap_reason"] = "unreconciled_claim"
		}
	}
	if len(breakdown) > 0 {
		out.Breakdown = breakdown
	}
	return out
}

// RecordSettlement stamps a session's settlement, advancing the per-session
// sequence. Authorization revisions do not reset this sequence.
func (e *Engine) RecordSettlement(ctx context.Context, sessionID string) (*pb.SettlementRecord, error) {
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return e.recordSettlementLocked(ctx, sessionID)
}

func (e *Engine) recordSettlementLocked(_ context.Context, sessionID string) (*pb.SettlementRecord, error) {
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	if rec.Terminal() && len(rec.TerminalSettlement) > 0 {
		return e.SettlementFor(rec, nil), nil
	}
	spec := acceptedSettlementSpec(rec, e.cfg.Specs(sessionID))
	if spec == nil {
		return nil, fmt.Errorf("settlement offering unavailable")
	}
	var out *pb.SettlementRecord
	err = e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		if r.Terminal() {
			if err := e.freezeTerminalSettlement(r, spec); err != nil {
				return err
			}
			out = e.SettlementFor(r, nil)
		} else {
			if r.SettlementSeq == math.MaxUint64 {
				return fmt.Errorf("settlement sequence exhausted")
			}
			r.SettlementSeq++
			out = e.SettlementFor(r, spec)
		}
		return nil
	})
	return out, err
}

// Called inside the terminal state transaction. The broker session sequence is
// independent from receiver advance/settlement sequences. Old terminal records
// are repaired through the same operation before their next publication.
func (e *Engine) freezeTerminalSettlement(r *sessionstore.Record, spec *OfferingSpec) error {
	if len(r.TerminalSettlement) > 0 {
		return nil
	}
	spec = acceptedSettlementSpec(r, spec)
	if spec == nil {
		return fmt.Errorf("settlement offering unavailable")
	}
	if r.SettlementSeq == math.MaxUint64 {
		return fmt.Errorf("settlement sequence exhausted")
	}
	r.SettlementSeq++
	record := e.SettlementFor(r, spec)
	if record == nil {
		return fmt.Errorf("settlement unavailable")
	}
	raw, err := proto.Marshal(record)
	if err != nil {
		return err
	}
	r.TerminalSettlement = raw
	return nil
}

// wireState maps this broker's internal session state onto the three
// the signed record is allowed to carry: "open", "winding_down" or
// "closed" (types.proto SettlementRecord.state).
//
// The internal set is active / winding_down / ended / failed, and it was
// being passed through verbatim. A terminal record therefore said
// "ended", which is not a value the proto defines, and a clearinghouse
// validating against the spec refused it as session_not_terminal —
// correctly. An interim record said "active" for the same reason; that
// half went unreported only because nobody had validated one yet.
//
// Both terminal states map to "closed". The wire field answers "may more
// value still be claimed against this session", and for a failed session
// the answer is no, exactly as for one that ended cleanly. WHY it ended
// is carried by termination_reason, which is where a reader should look
// — the state field has three values precisely so it cannot become an
// open-ended enum a consumer has to keep up with.
func wireState(internal string) string {
	switch internal {
	case sessionstore.StateActive:
		return "open"
	case sessionstore.StateWindingDown:
		return "winding_down"
	case sessionstore.StateEnded, sessionstore.StateFailed:
		return "closed"
	default:
		// An unmapped state is a bug in this broker, not a value to
		// invent a wire name for. "closed" is the conservative answer:
		// it tells a reader no further claim is coming, which is the
		// safe thing to be wrong about.
		return "closed"
	}
}

func decimalBytes(s string) []byte {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v == nil {
		return nil
	}
	return v.Bytes()
}

// A recovered initial open retains the signed price even when the offer is
// changed or removed before financial cleanup finishes. Historical records
// without this snapshot continue using their existing offering lookup.
func acceptedSettlementSpec(rec *sessionstore.Record, spec *OfferingSpec) *OfferingSpec {
	if rec.AcceptedPriceWei == "" {
		return spec
	}
	price, ok := new(big.Int).SetString(rec.AcceptedPriceWei, 10)
	if !ok || price.Sign() < 0 || rec.AcceptedPricePerUnits == 0 {
		return nil
	}
	return &OfferingSpec{PricePerWorkUnitWei: price, PerUnits: rec.AcceptedPricePerUnits}
}
