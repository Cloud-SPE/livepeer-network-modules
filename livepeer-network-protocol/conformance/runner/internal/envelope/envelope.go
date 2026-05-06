// Package envelope produces base64-encoded Livepeer-Payment envelopes for
// the conformance runner. Each driver calls Build with the fixture's
// capability/offering before sending the request and uses the returned
// string as the value of the Livepeer-Payment header.
//
// v0.1: ticket bytes are the literal string "conformance-runner-stub".
// Real probabilistic-micropayment ticket payloads come with the chain-
// integration follow-up.
package envelope

import (
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"

	pb "github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/proto-go/livepeer/payments/v1"
)

// stubTicket is the placeholder ticket payload used by the runner. The
// daemon accepts any non-empty value in v0.1.
var stubTicket = []byte("conformance-runner-stub")

// DefaultExpectedMaxUnits is the runner's chosen ceiling. Most fixtures
// debit much less than this; reconciliation handles the difference.
const DefaultExpectedMaxUnits uint64 = 1_000

// Build returns a base64-encoded Payment envelope for the given
// capability+offering pair, with `expected_max_units` defaulting to
// DefaultExpectedMaxUnits.
func Build(capability, offering string) (string, error) {
	return BuildWithMax(capability, offering, DefaultExpectedMaxUnits)
}

// BuildWithMax is Build with an explicit expected_max_units.
func BuildWithMax(capability, offering string, expectedMaxUnits uint64) (string, error) {
	if capability == "" {
		return "", fmt.Errorf("capability is empty")
	}
	if offering == "" {
		return "", fmt.Errorf("offering is empty")
	}
	if expectedMaxUnits == 0 {
		return "", fmt.Errorf("expectedMaxUnits must be > 0")
	}
	pay := &pb.Payment{
		CapabilityId:     capability,
		OfferingId:       offering,
		ExpectedMaxUnits: expectedMaxUnits,
		Ticket:           stubTicket,
	}
	raw, err := proto.Marshal(pay)
	if err != nil {
		return "", fmt.Errorf("marshal payment proto: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// SubstituteHeaders returns a new headers map with `<runner-generated-payment-blob>`
// replaced by a real base64-encoded Payment envelope built from the
// `Livepeer-Capability` / `Livepeer-Offering` header values in the same map.
//
// If neither header is present (e.g. a fixture exercising rejection paths),
// a placeholder header value is left as-is and not replaced.
func SubstituteHeaders(headers map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	const placeholder = "<runner-generated-payment-blob>"
	const livepeerCapability = "Livepeer-Capability"
	const livepeerOffering = "Livepeer-Offering"
	const livepeerPayment = "Livepeer-Payment"

	if out[livepeerPayment] != placeholder {
		return out, nil
	}
	cap := lookupCanonical(out, livepeerCapability)
	off := lookupCanonical(out, livepeerOffering)
	if cap == "" || off == "" {
		// Leave the placeholder; the fixture is exercising rejection.
		return out, nil
	}
	env, err := Build(cap, off)
	if err != nil {
		return nil, err
	}
	out[livepeerPayment] = env
	return out, nil
}

func lookupCanonical(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
