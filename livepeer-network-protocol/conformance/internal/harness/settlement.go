package harness

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify"
	"google.golang.org/protobuf/proto"
)

// settlementEnvelope is the wire shape of Livepeer-Settlement: a
// JCS-canonical payload plus an EIP-191 secp256k1 signature over exactly
// those bytes, the whole thing base64'd.
type settlementEnvelope struct {
	Payload   json.RawMessage `json:"payload"`
	Signature *struct {
		Algorithm        string `json:"algorithm"`
		Canonicalization string `json:"canonicalization"`
		Value            string `json:"value"`
	} `json:"signature,omitempty"`
}

// RecoverSettlementSigner returns the eth address that signed a
// base64 settlement envelope.
//
// The recovery runs over the payload bytes AS RECEIVED, never over a
// re-serialization of them: re-encoding is what silently repairs a
// broker that does not canonicalize, and the point of canonical
// signing is that the verifier and the signer agree on bytes rather
// than on an object model.
func RecoverSettlementSigner(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("settlement is not base64: %w", err)
	}
	var env settlementEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("settlement envelope is not JSON: %w", err)
	}
	if env.Signature == nil || env.Signature.Value == "" {
		return "", fmt.Errorf("settlement carries no signature; a clearinghouse must refuse it")
	}
	// The signature is 0x-prefixed hex, per livepeer-headers: the same
	// shape an eth_sign result has everywhere else, so a verifier that
	// already handles EIP-191 signatures needs no special case here.
	sig, err := hex.DecodeString(strings.TrimPrefix(env.Signature.Value, "0x"))
	if err != nil {
		return "", fmt.Errorf("signature value is not hex: %w", err)
	}
	addr, err := verify.New().Recover(env.Payload, sig)
	if err != nil {
		return "", fmt.Errorf("recover settlement signer: %w", err)
	}
	return string(addr), nil
}

// TamperSettlementUnits returns the envelope with actual_units altered
// and the signature left untouched — the forgery a verifier must catch.
func TamperSettlementUnits(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	var env settlementEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", err
	}
	var payload map[string]any
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return "", err
	}
	payload["actual_units"] = "999999"
	altered, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	env.Payload = altered
	out, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// SignedPaymentEnvelope builds a REAL payment envelope: a marshalled
// Payment whose expected_price carries the offering's price and the
// accepted-quote metadata a settlement record is built from.
//
// PaymentEnvelope's opaque stub is fine for every rule that treats the
// envelope as a token to be echoed, and it is what most of this suite
// wants. It is not enough to grade settlement: a broker cannot build a
// record without a price the sender signed, so an opaque envelope
// produces no record at all — which is why an entire suite could pass
// while nothing ever checked what the record said.
func SignedPaymentEnvelope(capability, offering, workUnit string, pricePerUnit, perUnits int64, estimatedUnits uint64, seed string) (string, error) {
	pay := &pb.Payment{
		Sender: []byte("conformance-sender-" + seed),
		ExpectedPrice: &pb.PriceInfo{
			PricePerUnit:  pricePerUnit,
			PixelsPerUnit: perUnits,
			Constraint: fmt.Sprintf("cap=%s;off=%s;wu=%s;est=%d;qid=conf-quote;qv=1;cfp=%s;rfp=%s",
				url.QueryEscape(capability), url.QueryEscape(offering), url.QueryEscape(workUnit),
				estimatedUnits, "aabb", "ccdd"),
		},
	}
	raw, err := proto.Marshal(pay)
	if err != nil {
		return "", fmt.Errorf("marshal payment: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}
