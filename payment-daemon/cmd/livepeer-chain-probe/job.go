package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func senderOf(m *pb.CreatePaymentResponse) []byte {
	var pay pb.Payment
	if err := proto.Unmarshal(m.GetPaymentBytes(), &pay); err != nil {
		return nil
	}
	return pay.GetSender()
}

// ---------------------------------------------------------------------------
// settlement envelope

type settlementEnvelope struct {
	payload   settlementPayload
	signature *settlementSignature
}

type settlementSignature struct {
	Algorithm        string `json:"algorithm"`
	Canonicalization string `json:"canonicalization"`
	Value            string `json:"value"`
}

// settlementPayload decodes only what the probe asserts on. Fields the
// probe does not check are deliberately absent rather than mirrored, so
// this does not quietly become a second schema to maintain.
type settlementPayload struct {
	JobID            string `json:"job_id"`
	WorkID           string `json:"work_id"`
	SessionID        string `json:"session_id"`
	AuthorizationID  string `json:"authorization_id"`
	IssuedAt         string `json:"issued_at"`
	State            string `json:"state"`
	Outcome          string `json:"outcome"`
	DebitedUnits     string `json:"debited_units"`
	GatewaySessionID string `json:"gateway_session_id"`
	// RequestID is the paid-job counterpart to GatewaySessionID: the id
	// the CALLER chose, as opposed to the two the broker minted.
	RequestID      string   `json:"request_id"`
	BilledValueWei bigValue `json:"billed_value_wei"`
}

// bigValue mirrors the proto BigUInt as protojson renders it: an object
// with a base64 `value`.
type bigValue struct {
	Value string `json:"value"`
}

func (b bigValue) value() []byte {
	raw, err := base64.StdEncoding.DecodeString(b.Value)
	if err != nil {
		return nil
	}
	return raw
}

func fetchSettlement(brokerURL, id string) (*settlementEnvelope, error) {
	resp, err := http.Get(brokerURL + "/v1/settlement/" + id)
	if err != nil {
		return nil, fmt.Errorf("settlement query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("settlement query status %d: %s", resp.StatusCode, readAll(resp.Body))
	}
	encoded := resp.Header.Get("Livepeer-Settlement")
	if encoded == "" {
		return nil, fmt.Errorf("settlement query returned no Livepeer-Settlement")
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("settlement is not base64: %w", err)
	}
	var env struct {
		Payload   json.RawMessage      `json:"payload"`
		Signature *settlementSignature `json:"signature"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("settlement envelope: %w", err)
	}
	var payload settlementPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return nil, fmt.Errorf("settlement payload: %w", err)
	}
	return &settlementEnvelope{payload: payload, signature: env.Signature}, nil
}
