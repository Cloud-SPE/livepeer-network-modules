package payment

import (
	"encoding/base64"
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// DecodeError represents a malformed Livepeer-Payment header value. The
// caller should reject with `payment_invalid` (HTTP 401) on this error.
type DecodeError struct{ Cause error }

func (e *DecodeError) Error() string { return fmt.Sprintf("decode payment envelope: %v", e.Cause) }
func (e *DecodeError) Unwrap() error { return e.Cause }

// MismatchError is returned when a decoded envelope's capability_id /
// offering_id disagrees with the request's Livepeer-Capability /
// Livepeer-Offering headers. Callers should reject with
// `payment_envelope_mismatch` (HTTP 401).
type MismatchError struct{ Reason string }

func (e *MismatchError) Error() string { return e.Reason }

// DecodeEnvelope parses the base64-encoded Livepeer-Payment header value
// into a Payment proto. Returns a *DecodeError if the value is malformed.
func DecodeEnvelope(headerValue string) (*pb.Payment, error) {
	if headerValue == "" {
		return nil, &DecodeError{Cause: errors.New("header value is empty")}
	}
	raw, err := base64.StdEncoding.DecodeString(headerValue)
	if err != nil {
		return nil, &DecodeError{Cause: fmt.Errorf("base64: %w", err)}
	}
	var pay pb.Payment
	if err := proto.Unmarshal(raw, &pay); err != nil {
		return nil, &DecodeError{Cause: fmt.Errorf("protobuf: %w", err)}
	}
	if pay.GetCapabilityId() == "" {
		return nil, &DecodeError{Cause: errors.New("capability_id is empty")}
	}
	if pay.GetOfferingId() == "" {
		return nil, &DecodeError{Cause: errors.New("offering_id is empty")}
	}
	if pay.GetExpectedMaxUnits() == 0 {
		return nil, &DecodeError{Cause: errors.New("expected_max_units must be > 0")}
	}
	if len(pay.GetTicket()) == 0 {
		return nil, &DecodeError{Cause: errors.New("ticket is empty")}
	}
	return &pay, nil
}

// CrossCheck enforces that the envelope's capability_id / offering_id
// match the request's Livepeer-Capability / Livepeer-Offering headers.
// Returns a *MismatchError on disagreement.
func CrossCheck(env *pb.Payment, headerCapability, headerOffering string) error {
	if env.GetCapabilityId() != headerCapability {
		return &MismatchError{Reason: fmt.Sprintf(
			"capability_id mismatch: header=%q envelope=%q",
			headerCapability, env.GetCapabilityId())}
	}
	if env.GetOfferingId() != headerOffering {
		return &MismatchError{Reason: fmt.Sprintf(
			"offering_id mismatch: header=%q envelope=%q",
			headerOffering, env.GetOfferingId())}
	}
	return nil
}
