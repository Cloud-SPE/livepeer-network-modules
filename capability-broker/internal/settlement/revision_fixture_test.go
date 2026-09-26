package settlement

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"os"
	"strings"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
)

// A public, deterministic test key; never use it for a deployment. This golden
// file is consumable by LOC without running Go or the broker.
func TestRevisionContractFixture(t *testing.T) {
	key, err := crypto.HexToECDSA(strings.Repeat("0", 63) + "1")
	if err != nil {
		t.Fatal(err)
	}
	signer := &Signer{key: key}
	value := func(decimal string) *pb.BigUInt {
		n, ok := new(big.Int).SetString(decimal, 10)
		if !ok {
			t.Fatal(decimal)
		}
		return &pb.BigUInt{Value: n.Bytes()}
	}
	hash := sha256.Sum256([]byte("fixture authorization wire; not an executable spend grant"))
	quote := &pb.QuoteRef{QuoteId: "quote-fixture", QuoteVersion: 1, ConstraintFingerprint: bytes.Repeat([]byte{3}, 32), RouteFingerprint: bytes.Repeat([]byte{4}, 32)}
	revision := &pb.SessionRevisionRecord{WholesaleAccountId: "loc-prod", EvidenceDomain: "livepeer-session-revision/v1", Protocol: "paid-session/v1", SettlementDomainId: "fixture-domain", BrokerUri: "https://broker.example", SessionId: "sess-fixture", GatewaySessionId: "loc-fixture", RequestId: "refill-fixture", Payer: bytes.Repeat([]byte{1}, 20), Payee: bytes.Repeat([]byte{2}, 20), AuthorizationId: "successor-fixture", PredecessorAuthorizationId: "predecessor-fixture", Revision: 1, AuthorizationFingerprint: hash[:], AcceptedQuoteRef: quote, Outcome: pb.SessionRevisionRecord_NOT_ADMITTED, NonAdmissionBasis: "receiver_fenced", Reason: "INSUFFICIENT_WHOLESALE_CREDIT", Stage: "admission", ReceiverCode: "FailedPrecondition", ObservedAt: "2026-09-25T12:00:00Z", MaxTotalUnits: 180, MaxDebitWei: value("180000000000000")}
	refused, err := EncodeRevision(revision, signer)
	if err != nil {
		t.Fatal(err)
	}
	terminal := &pb.SettlementRecord{WholesaleAccountId: "loc-prod", SettlementDomainId: "fixture-domain", SessionId: "sess-fixture", GatewaySessionId: "loc-fixture", WorkId: "predecessor-fixture", AuthorizationId: "predecessor-fixture", AcceptedQuoteRef: quote, WorkUnitName: "output_seconds", ClaimedUnits: 124, DebitedUnits: 120, ActualUnits: 120, BilledUnits: 120, BilledValueWei: value("120000000000000"), AmountWei: value("1000000000000"), PerUnits: 1, AuthorizedValueWei: value("120000000000000"), SettlementSeq: 1, IssuedAt: "2026-09-25T12:00:20Z", State: "closed", Breakdown: map[string]string{"termination_reason": "authorization_exhausted", "claim_debit_gap": "true", "claim_debit_gap_reason": "authorization_cap"}}
	closed, err := Encode(terminal, signer)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(encoded string) json.RawMessage {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	fixture := map[string]any{"contract": "paid-session/v1", "description": "Synthetic public-key fixture; authorization hash is an opaque test binding, not a spendable grant. Exact terminal retries are idempotent, sequence zero and conflicting same-sequence evidence are rejected.", "signer_address": crypto.PubkeyToAddress(key.PublicKey).Hex(), "signer_public_key": signer.PublicKeyHex(), "revision_not_admitted": map[string]any{"http_status": 200, "header": refused, "envelope": decode(refused)}, "terminal_cap_overshoot": map[string]any{"http_status": 200, "header": closed, "envelope": decode(closed), "receiver_sequence": 15, "expected_bill_wei": "120000000000000", "repeat_lookup_and_close_header": closed}, "revision_pending": map[string]any{"http_status": 202, "outcome": "pending", "signed_evidence": false}, "canceled_unused": map[string]any{"durable": true, "reversible": false, "readmission_allowed": false}}
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	path := "../../../livepeer-network-protocol/conformance/fixtures/session-revision-evidence.json"
	if os.Getenv("UPDATE_CONTRACT_FIXTURES") == "1" {
		if err := os.WriteFile(path, raw, 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, want) {
		t.Fatal("contract fixture differs; regenerate intentionally with UPDATE_CONTRACT_FIXTURES=1")
	}
}
