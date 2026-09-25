package settlement

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestRevisionEvidenceRequiresProofAndBindsScope(t *testing.T) {
	signer := testSigner(t)
	record := &pb.SessionRevisionRecord{EvidenceDomain: "livepeer-session-revision/v1", Protocol: "paid-session/v1", SessionId: "broker-session", GatewaySessionId: "loc-session", RequestId: "refill", AuthorizationId: "successor", PredecessorAuthorizationId: "predecessor", SettlementDomainId: "domain", Payer: []byte{1}, Payee: []byte{2}, AuthorizationFingerprint: []byte{3}, Outcome: pb.SessionRevisionRecord_NOT_ADMITTED}
	if _, err := EncodeRevision(record, signer); err == nil {
		t.Fatal("non-admission without receiver proof signed")
	}
	record.NonAdmissionBasis = "receiver_fenced"
	if _, err := EncodeRevision(record, nil); err == nil {
		t.Fatal("unsigned evidence emitted")
	}
	encoded, err := EncodeRevision(record, signer)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(encoded)
	var env Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	sig, err := decodeHex(env.Signature.Value)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	verify := func(payload []byte) bool {
		pub, err := crypto.SigToPub(personalSignDigest(payload), sig)
		return err == nil && "0x"+hexEncode(crypto.FromECDSAPub(pub)) == signer.PublicKeyHex()
	}
	if !verify(env.Payload) {
		t.Fatal("original signature invalid")
	}
	for _, mutate := range []func(*pb.SessionRevisionRecord){
		func(r *pb.SessionRevisionRecord) { r.AuthorizationId = "other" }, func(r *pb.SessionRevisionRecord) { r.SessionId = "other" }, func(r *pb.SessionRevisionRecord) { r.Payer = []byte{4} }, func(r *pb.SessionRevisionRecord) { r.SettlementDomainId = "other" }, func(r *pb.SessionRevisionRecord) { r.Outcome = pb.SessionRevisionRecord_ADMITTED }, func(r *pb.SessionRevisionRecord) { r.AuthorizationFingerprint = []byte{5} },
	} {
		changed := proto.Clone(record).(*pb.SessionRevisionRecord)
		mutate(changed)
		body, _ := protojson.MarshalOptions{UseProtoNames: true}.Marshal(changed)
		canonical, err := canonicalize(body)
		if err != nil {
			t.Fatal(err)
		}
		if verify(canonical) {
			t.Fatal("signature accepted modified scope")
		}
	}
	if strings.Contains(string(env.Payload), "authorization_bytes") {
		t.Fatal("private wire material exposed")
	}
}
