package settlement

import (
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
)

func testSigner(t *testing.T) *Signer {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settlement.key")
	if err := os.WriteFile(path, []byte("0x"+strings.TrimSpace(hexKey(t, key))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func hexKey(t *testing.T, key *ecdsa.PrivateKey) string {
	t.Helper()
	return hexEncode(crypto.FromECDSA(key))
}

func hexEncode(b []byte) string { return hex.EncodeToString(b) }

func decodeHex(s string) ([]byte, error) {
	return hex.DecodeString(strings.TrimPrefix(s, "0x"))
}

func sample() *pb.SettlementRecord {
	return &pb.SettlementRecord{
		SessionId:          "sess_1",
		WorkId:             "b3d1",
		PredecessorWorkId:  "a09c",
		RotationGeneration: 1,
		WorkUnitName:       "participant_minutes",
		ClaimedUnits:       31,
		DebitedUnits:       31,
		PerUnits:           1000,
		SettlementSeq:      4,
		IssuedAt:           "2026-08-20T12:00:00Z",
		State:              "closed",
	}
}

// TestCanonicalPayloadIsStable: protojson deliberately varies its
// whitespace, so a signature over its raw output would verify only
// sometimes. Canonicalization is what makes the bytes reproducible.
func TestCanonicalPayloadIsStable(t *testing.T) {
	var first []byte
	for i := 0; i < 50; i++ {
		got, err := CanonicalPayload(sample())
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = got
			continue
		}
		if string(got) != string(first) {
			t.Fatalf("canonical bytes vary between runs:\n%s\n%s", first, got)
		}
	}
	if strings.Contains(string(first), " ") {
		t.Fatalf("canonical form contains whitespace: %s", first)
	}
}

// TestSignatureRecoversToTheSigningKey: the property a consumer relies
// on — a record verifies against the public key the orch delegated in
// its manifest, and nothing else.
func TestSignatureRecoversToTheSigningKey(t *testing.T) {
	signer := testSigner(t)
	encoded, err := Encode(sample(), signer)
	if err != nil {
		t.Fatal(err)
	}
	rawEnv, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var env Envelope
	if err := json.Unmarshal(rawEnv, &env); err != nil {
		t.Fatal(err)
	}
	if env.Signature == nil {
		t.Fatal("envelope carries no signature")
	}
	if env.Signature.Algorithm != Alg || env.Signature.Canonicalization != Canonicalization {
		t.Fatalf("scheme = %+v; a consumer must not have to infer it", env.Signature)
	}

	sig, err := decodeHex(env.Signature.Value)
	if err != nil {
		t.Fatal(err)
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(personalSignDigest(env.Payload), sig)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if got := "0x" + hexEncode(crypto.FromECDSAPub(pub)); got != signer.PublicKeyHex() {
		t.Fatalf("recovered %s; want the signing key %s", got, signer.PublicKeyHex())
	}
}

// TestTamperedPayloadDoesNotVerify: the point of signing.
func TestTamperedPayloadDoesNotVerify(t *testing.T) {
	signer := testSigner(t)
	encoded, err := Encode(sample(), signer)
	if err != nil {
		t.Fatal(err)
	}
	rawEnv, _ := base64.StdEncoding.DecodeString(encoded)
	var env Envelope
	if err := json.Unmarshal(rawEnv, &env); err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(env.Payload), `"31"`, `"3100"`, 1)
	if tampered == string(env.Payload) {
		t.Fatal("test did not alter the payload")
	}
	sig, _ := decodeHex(env.Signature.Value)
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(personalSignDigest([]byte(tampered)), sig)
	if err == nil && "0x"+hexEncode(crypto.FromECDSAPub(pub)) == signer.PublicKeyHex() {
		t.Fatal("a tampered payload recovered to the signing key")
	}
}

// TestUnsignedEnvelopeOmitsSignature: a broker with no delegated key
// still reports what it billed, and the absent field is what tells a
// consumer to reject it for anything financially material.
func TestUnsignedEnvelopeOmitsSignature(t *testing.T) {
	encoded, err := Encode(sample(), nil)
	if err != nil {
		t.Fatal(err)
	}
	rawEnv, _ := base64.StdEncoding.DecodeString(encoded)
	if strings.Contains(string(rawEnv), "signature") {
		t.Fatalf("unsigned envelope mentions a signature: %s", rawEnv)
	}
}
