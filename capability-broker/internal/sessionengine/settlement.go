package sessionengine

import (
	"context"
	"math/big"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// Settlement for paid-session/v1.
//
// A job settles in one exchange against one payment identity, so its
// record is built from the payment envelope alone. A session outlives
// many payments and may change identity mid-flight, so its record is
// built from the broker's own durable state: the cumulative totals it
// kept, the generation chain a rotation left behind, and the price the
// session was pinned to at open.
//
// The authoritative billing quantity is cumulative debited_units — what
// the ledger moved — not claimed_units, which is only what a runner
// asserted. The two cannot diverge in this engine (the debit is issued
// before the commit and both totals advance in one store update), so a
// record where they differ is a defect to assert on rather than an
// accounting subtlety to interpret.

// SettlementFor builds the settlement record for a session as of now.
// State distinguishes an interim snapshot from a final settlement, so a
// reader can tell "this session is still running" from "this is what it
// cost".
func (e *Engine) SettlementFor(rec *sessionstore.Record, spec *OfferingSpec) *pb.SettlementRecord {
	if rec == nil || spec == nil {
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

	// One ceiling over the cumulative total — never a sum of
	// per-generation ceilings, which would reintroduce the per-chunk
	// rounding drift the cumulative rule exists to prevent.
	// What the ledger charged, summed across this session's debits. A
	// ceiling recomputed over the session's own total is right only when
	// the payment session is not shared — and it is shared whenever a
	// gateway mints more than once from one ticket session.
	billed := payment.BillFor(amount, perUnits, rec.DebitedTotal)
	if v, ok := new(big.Int).SetString(rec.BilledWei, 10); ok && v != nil && v.Sign() > 0 {
		billed = v
	}
	generationUnits := rec.DebitedTotal - rec.GenerationStartUnits
	generationBilled := new(big.Int).Sub(billed, payment.BillFor(amount, perUnits, rec.GenerationStartUnits))

	out := &pb.SettlementRecord{
		WorkUnitName:   rec.Unit,
		ActualUnits:    rec.DebitedTotal,
		BilledUnits:    rec.DebitedTotal,
		BilledValueWei: &pb.BigUInt{Value: billed.Bytes()},

		SessionId: rec.SessionID,
		// The consumer's own identifier. session_id is broker-local and
		// work_id can be shared, so this is the only field that binds a
		// record to the session a clearinghouse issued.
		GatewaySessionId:   rec.GatewaySessionID,
		WorkId:             rec.WorkID,
		PredecessorWorkId:  rec.PredecessorWorkID,
		RotationGeneration: rec.RotationGeneration,

		ClaimedUnits: rec.ClaimedTotal,
		DebitedUnits: rec.DebitedTotal,
		// Where this session's last debit landed on the shared identity's
		// curve. Not this session's total — see the proto comment for
		// what it does and does not let a reader verify.
		PaymentCumulativeUnits: rec.PaymentCumulativeUnits,

		GenerationDebitedUnits:   generationUnits,
		GenerationBilledValueWei: &pb.BigUInt{Value: generationBilled.Bytes()},

		FundedValueWei:           &pb.BigUInt{Value: decimalBytes(rec.FundedWei)},
		GenerationFundedValueWei: &pb.BigUInt{Value: decimalBytes(rec.GenerationFundedWei)},

		AmountWei: &pb.BigUInt{Value: amount.Bytes()},
		PerUnits:  perUnits,

		SettlementSeq: rec.SettlementSeq,
		IssuedAt:      e.cfg.Now().UTC().Format(time.RFC3339Nano),
		State:         rec.State,
	}
	if rec.ClaimedTotal != rec.DebitedTotal {
		// Recorded rather than smoothed over: the two advance in one
		// commit, so a gap is a bug in this broker and a reader should
		// treat it the way it treats a bad signature.
		out.Breakdown = map[string]string{"claim_debit_gap": "true"}
	}
	return out
}

// RecordSettlement stamps a session's settlement, advancing the
// per-session sequence. settlement_seq is monotonic per session_id, not
// per work_id: a rotation mints a new work_id, and a per-identity
// counter would restart mid-session and leave a reader unable to order
// two records from one session.
func (e *Engine) RecordSettlement(ctx context.Context, sessionID string) (*pb.SettlementRecord, error) {
	mu := e.sessionMu(sessionID)
	mu.Lock()
	defer mu.Unlock()
	return e.recordSettlementLocked(ctx, sessionID)
}

func (e *Engine) recordSettlementLocked(_ context.Context, sessionID string) (*pb.SettlementRecord, error) {
	var seq uint64
	if err := e.cfg.Store.Update(sessionID, func(r *sessionstore.Record) error {
		r.SettlementSeq++
		seq = r.SettlementSeq
		return nil
	}); err != nil {
		return nil, err
	}
	rec, err := e.cfg.Store.Get(sessionID)
	if err != nil {
		return nil, err
	}
	spec := e.cfg.Specs(sessionID)
	if spec == nil {
		return nil, nil
	}
	out := e.SettlementFor(rec, spec)
	if out == nil {
		return nil, nil
	}
	out.SettlementSeq = seq
	return out, nil
}

func decimalBytes(s string) []byte {
	v, ok := new(big.Int).SetString(s, 10)
	if !ok || v == nil {
		return nil
	}
	return v.Bytes()
}
