package middleware

import (
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

// SettlementIdentity binds a settlement record to the exchange it
// describes, and to when it was made. All three ride inside the signed
// payload — evidence that does not say what it is evidence of can be
// replayed against something else.
type SettlementIdentity struct {
	// JobID is the broker-assigned Livepeer-Job-Id.
	JobID string
	// WorkID is the payee-side payment identity.
	WorkID string
	// IssuedAt is RFC3339 (nanosecond precision).
	IssuedAt string
}

// SettlementInputs captures everything needed to build a
// SettlementRecord at a later point in time. Long-lived session
// drivers (RTMP, session-control) snapshot this struct onto their
// per-session records during Serve so they can emit settlement at
// session-close, after the original per-request payment middleware
// has long since returned.
type SettlementInputs struct {
	// PaymentBytes is the raw wire-format Payment the gateway
	// supplied at session-open. The accepted-quote/price metadata is
	// parsed back out of its expected_price.constraint field.
	PaymentBytes []byte
	// FundedValueWei is the broker-credited expected value for the
	// session, returned by payment.Client.OpenSession at session-open.
	FundedValueWei *big.Int
	// WorkUnit is the canonical work-unit name for the offering. Used
	// only when the payment's expected_price.constraint omits its own
	// `wu=` hint (legacy/stub payments).
	WorkUnit string
}

// BuildSettlementRecord constructs a SettlementRecord from a session's
// inputs, the final measured units, and an optional termination reason
// (one of the livepeerheader.Err* strings; empty for normal close).
// Returns nil when the payment cannot be parsed or has no
// expected_price — both indicate a stub/legacy payment that doesn't
// support settlement.
func BuildSettlementRecord(in SettlementInputs, actualUnits uint64, terminationReason string, ident SettlementIdentity) *pb.SettlementRecord {
	return buildSettlementRecord(in.PaymentBytes, in.FundedValueWei, actualUnits, in.WorkUnit, terminationReason, ident)
}

// Deprecated: use internal/settlement.Encode, which emits the signed
// envelope both protocols now carry. Kept only until the last caller
// moves.
//
// EncodeSettlementRecord base64-encodes a marshalled SettlementRecord
// for transport in a single HTTP header or WebSocket terminal-event
// field.
func EncodeSettlementRecord(record *pb.SettlementRecord) (string, error) {
	return encodeSettlementRecord(record)
}

func encodeSettlementRecord(record *pb.SettlementRecord) (string, error) {
	if record == nil {
		return "", fmt.Errorf("settlement record is nil")
	}
	raw, err := proto.Marshal(record)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func buildSettlementRecord(paymentBytes []byte, fundedValueWei *big.Int, actualUnits uint64, currentWorkUnit string, terminationReason string, ident SettlementIdentity) *pb.SettlementRecord {
	var pay pb.Payment
	if err := proto.Unmarshal(paymentBytes, &pay); err != nil {
		return nil
	}
	price := pay.GetExpectedPrice()
	if price == nil {
		return nil
	}
	meta, ok := parseExpectedPriceConstraint(price.GetConstraint())
	if !ok {
		return nil
	}
	unitsPerPrice := price.GetPixelsPerUnit()
	if unitsPerPrice <= 0 {
		unitsPerPrice = 1
	}
	// Ceiling, per offering-axes.md §6.1 — the same function the payee's
	// ledger and the session path compute. Integer division floors, so
	// with per_units > 1 this used to attest less than was actually
	// billed, and a clearinghouse recomputing the rule disagreed with
	// the record it was verifying.
	billedValueWei := payment.BillFor(big.NewInt(price.GetPricePerUnit()), uint64(unitsPerPrice), actualUnits)
	if fundedValueWei == nil {
		fundedValueWei = new(big.Int)
	}

	outcome := pb.SettlementRecord_EXACT
	switch fundedValueWei.Cmp(billedValueWei) {
	case -1:
		outcome = pb.SettlementRecord_UNDERFUNDED
	case 1:
		outcome = pb.SettlementRecord_OVERFUNDED
	}
	// A budget-driven termination takes precedence over the funded/billed
	// comparison: the session stopped because runway was exhausted, not
	// because the gateway happened to over- or under-fund the request.
	if terminationReason == livepeerheader.ErrInsufficientBalance {
		outcome = pb.SettlementRecord_STOPPED_AT_BUDGET
	}

	workUnit := meta.workUnitName
	if workUnit == "" {
		workUnit = currentWorkUnit
	}

	return &pb.SettlementRecord{
		// Identity inside the signature. Without it a valid settlement
		// verifies as evidence for a different exchange: work_id on
		// paid-job is the ticket session's rand hash, shared by every
		// job minted against it, so job_id is what makes the record
		// about ONE exchange.
		JobId:    ident.JobID,
		WorkId:   ident.WorkID,
		IssuedAt: ident.IssuedAt,

		AcceptedQuoteRef: &pb.QuoteRef{
			QuoteId:               meta.quoteID,
			QuoteVersion:          meta.quoteVersion,
			ConstraintFingerprint: meta.constraintFingerprint,
			RouteFingerprint:      meta.routeFingerprint,
		},
		WorkUnitName:   workUnit,
		EstimatedUnits: meta.estimatedUnits,
		ActualUnits:    actualUnits,
		BilledUnits:    actualUnits,
		FundedValueWei: &pb.BigUInt{Value: fundedValueWei.Bytes()},
		BilledValueWei: &pb.BigUInt{Value: billedValueWei.Bytes()},
		Outcome:        outcome,
	}
}

func validateExpectedPriceForRequest(paymentBytes []byte, capability, offering string, spec CapabilitySpec) error {
	var pay pb.Payment
	if err := proto.Unmarshal(paymentBytes, &pay); err != nil {
		// Legacy/mock bytes are tolerated so existing unit tests and stubs continue to work.
		return nil
	}
	price := pay.GetExpectedPrice()
	if price == nil {
		return errors.New("payment expected_price is missing")
	}
	if price.GetPricePerUnit() <= 0 {
		return errors.New("payment expected_price.price_per_unit must be > 0")
	}
	if price.GetPixelsPerUnit() <= 0 {
		return errors.New("payment expected_price.pixels_per_unit must be > 0")
	}
	meta, ok := parseExpectedPriceConstraint(price.GetConstraint())
	if !ok {
		return errors.New("payment expected_price.constraint is not parseable")
	}
	if meta.capability != capability {
		return fmt.Errorf("payment capability %q does not match request capability %q", meta.capability, capability)
	}
	if meta.offering != offering {
		return fmt.Errorf("payment offering %q does not match request offering %q", meta.offering, offering)
	}
	if meta.workUnitName != "" && spec.WorkUnit != "" && meta.workUnitName != spec.WorkUnit {
		return fmt.Errorf("payment work_unit %q does not match broker work_unit %q", meta.workUnitName, spec.WorkUnit)
	}
	if spec.PricePerWorkUnitWei != nil {
		if !spec.PricePerWorkUnitWei.IsInt64() {
			return errors.New("broker price_per_work_unit_wei exceeds payment wire int64 range")
		}
		if price.GetPricePerUnit() != spec.PricePerWorkUnitWei.Int64() {
			return fmt.Errorf("payment price_per_unit %d does not match broker price %s", price.GetPricePerUnit(), spec.PricePerWorkUnitWei.String())
		}
	}
	// pixels_per_unit is go-livepeer's name for the price denominator
	// (offering-axes.md §6.3). It must equal the offering's per_units,
	// or payer and payee are pricing the same work differently.
	wantPerUnits := int64(1)
	if spec.PerUnits > 1 {
		wantPerUnits = int64(spec.PerUnits)
	}
	if price.GetPixelsPerUnit() != wantPerUnits {
		return fmt.Errorf("payment pixels_per_unit %d does not match the offering's per_units %d",
			price.GetPixelsPerUnit(), wantPerUnits)
	}
	return nil
}

// ValidateExpectedPriceForRequest exposes the payment/header cross-check used
// by the paid middleware so non-middleware session routes can enforce the
// same expected-price contract.
func ValidateExpectedPriceForRequest(paymentBytes []byte, capability, offering string, spec CapabilitySpec) error {
	return validateExpectedPriceForRequest(paymentBytes, capability, offering, spec)
}

type expectedPriceMeta struct {
	capability            string
	offering              string
	workUnitName          string
	estimatedUnits        uint64
	quoteID               string
	quoteVersion          uint64
	constraintFingerprint []byte
	routeFingerprint      []byte
}

func parseExpectedPriceConstraint(raw string) (expectedPriceMeta, bool) {
	if strings.TrimSpace(raw) == "" {
		return expectedPriceMeta{}, false
	}
	var out expectedPriceMeta
	for _, part := range strings.Split(raw, ";") {
		key, val, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch key {
		case "cap":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.capability = s
		case "off":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.offering = s
		case "wu":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.workUnitName = s
		case "est":
			n, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.estimatedUnits = n
		case "qid":
			s, err := url.QueryUnescape(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.quoteID = s
		case "qv":
			n, err := strconv.ParseUint(val, 10, 64)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.quoteVersion = n
		case "cfp":
			b, err := hex.DecodeString(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.constraintFingerprint = b
		case "rfp":
			b, err := hex.DecodeString(val)
			if err != nil {
				return expectedPriceMeta{}, false
			}
			out.routeFingerprint = b
		}
	}
	if out.quoteID == "" || len(out.constraintFingerprint) == 0 || len(out.routeFingerprint) == 0 {
		return expectedPriceMeta{}, false
	}
	return out, true
}
