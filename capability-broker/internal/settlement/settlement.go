// Package settlement encodes and signs the broker-authoritative
// settlement record.
//
// A settlement crosses a customer-controlled SDK on its way to a
// clearinghouse, so channel authentication ends before the record
// arrives. Its integrity has to travel with it: the record is signed,
// and a consumer verifies it against a key the orch's cold key delegated
// through the signed manifest (manifest/schema.json settlement_keys).
//
// The signing key is a HOT key held by the broker, never the orch's cold
// key. A broker is network-exposed; compromising one must not cost an
// operator the identity that anchors it on-chain.
//
// Canonicalization is JCS (RFC 8785) over the record's protojson form,
// and the signature is secp256k1 under the EIP-191 personal-sign
// envelope — the same pair the manifest uses, so a consumer verifies
// settlement with the primitives it already has rather than a second
// scheme. protojson's output is deliberately unstable (it varies
// whitespace); canonicalization normalizes that away, and taking the
// field names from the proto means the signed shape cannot drift from
// the record.
package settlement

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/encoding/protojson"
)

// Alg and Canonicalization name the scheme in the envelope, so a
// consumer never has to infer it.
const (
	Alg              = "secp256k1"
	Canonicalization = "jcs"
)

// Envelope is the wire form of a settlement: the canonical payload and
// the signature over exactly those bytes.
type Envelope struct {
	Payload   json.RawMessage `json:"payload"`
	Signature *Signature      `json:"signature,omitempty"`
}

// Signature is EIP-191 personal-sign over the canonical payload bytes.
type Signature struct {
	Algorithm        string `json:"algorithm"`
	Canonicalization string `json:"canonicalization"`
	Value            string `json:"value"` // 0x-prefixed 130-hex
}

// Signer holds the broker's delegated settlement key and the validity
// window the orch published for it.
type Signer struct {
	key *ecdsa.PrivateKey
	// notBefore/expiresAt mirror the manifest's settlement_keys entry.
	// A record signed outside the window is one no consumer will accept,
	// so the broker refuses to produce it rather than emitting evidence
	// that fails verification somewhere else, later, for reasons the
	// operator cannot see from here.
	notBefore time.Time
	expiresAt time.Time
	now       func() time.Time
}

// SetValidity records the delegation window this key was published with.
// Zero times mean unbounded, which is what a deployment that has not
// published a delegation yet has.
func (s *Signer) SetValidity(notBefore, expiresAt time.Time) {
	if s == nil {
		return
	}
	s.notBefore, s.expiresAt = notBefore, expiresAt
}

func (s *Signer) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now().UTC()
}

// ErrKeyOutsideValidity reports a signing attempt outside the published
// window.
var ErrKeyOutsideValidity = errors.New("settlement: signing key is outside its published validity window")

// LoadSigner reads a hex-encoded secp256k1 private key from path. The
// file holds the key with optional 0x prefix and surrounding whitespace,
// matching how the session store's sealing key is carried.
func LoadSigner(path string) (*Signer, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("settlement key: %w", err)
	}
	text := strings.TrimSpace(string(raw))
	text = strings.TrimPrefix(strings.TrimPrefix(text, "0x"), "0X")
	key, err := crypto.HexToECDSA(text)
	if err != nil {
		return nil, fmt.Errorf("settlement key: %w", err)
	}
	return &Signer{key: key}, nil
}

// PublicKeyHex is the uncompressed public key an operator publishes in
// the manifest's settlement_keys block.
func (s *Signer) PublicKeyHex() string {
	if s == nil || s.key == nil {
		return ""
	}
	return "0x" + hex.EncodeToString(crypto.FromECDSAPub(&s.key.PublicKey))
}

// Sign returns the EIP-191 personal-sign signature over canonical.
func (s *Signer) Sign(canonical []byte) (string, error) {
	if s == nil || s.key == nil {
		return "", fmt.Errorf("settlement: no signing key")
	}
	now := s.clock()
	if !s.notBefore.IsZero() && now.Before(s.notBefore) {
		return "", fmt.Errorf("%w: not_before=%s", ErrKeyOutsideValidity, s.notBefore.Format(time.RFC3339))
	}
	if !s.expiresAt.IsZero() && now.After(s.expiresAt) {
		return "", fmt.Errorf("%w: expires_at=%s", ErrKeyOutsideValidity, s.expiresAt.Format(time.RFC3339))
	}
	sig, err := crypto.Sign(personalSignDigest(canonical), s.key)
	if err != nil {
		return "", fmt.Errorf("settlement: sign: %w", err)
	}
	// Normalize v to {27,28}: go-ethereum signs with {0,1}, and every
	// EIP-191 verifier accepts the 27-based form.
	if len(sig) == 65 && sig[64] < 27 {
		sig[64] += 27
	}
	return "0x" + hex.EncodeToString(sig), nil
}

// Encode canonicalizes a record, signs it when a signer is configured,
// and returns the base64 envelope for the Livepeer-Settlement header.
//
// A nil signer yields an unsigned envelope rather than an error: a
// broker running against a mock payment layer has no delegated key and
// still needs to report what it billed. A consumer that requires
// integrity MUST reject an envelope with no signature — which is why the
// field is absent rather than empty.
func Encode(rec *pb.SettlementRecord, signer *Signer) (string, error) {
	canonical, err := CanonicalPayload(rec)
	if err != nil {
		return "", err
	}
	env := Envelope{Payload: canonical}
	if signer != nil {
		value, err := signer.Sign(canonical)
		if err != nil {
			return "", err
		}
		env.Signature = &Signature{
			Algorithm:        Alg,
			Canonicalization: Canonicalization,
			Value:            value,
		}
	}
	out, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("settlement: marshal envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// CanonicalPayload renders a record as the exact bytes that get signed.
func CanonicalPayload(rec *pb.SettlementRecord) (json.RawMessage, error) {
	if rec == nil {
		return nil, fmt.Errorf("settlement: nil record")
	}
	// UseProtoNames keeps the JSON keys identical to the proto field
	// names, so the signed shape is the schema rather than a mapping
	// somebody has to maintain alongside it.
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("settlement: marshal record: %w", err)
	}
	return canonicalize(raw)
}

// canonicalize is JCS (RFC 8785) with stdlib only: decode preserving
// number literals, re-emit with object keys sorted and no whitespace.
// Mirrors orch-coordinator's CanonicalBytes so both ends of a manifest
// and a settlement canonicalize the same way.
func canonicalize(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("settlement: canonicalize: %w", err)
	}
	var buf bytes.Buffer
	if err := writeCanonical(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			enc, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(enc)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []any:
		buf.WriteByte('[')
		for i, item := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		enc, err := json.Marshal(v)
		if err != nil {
			return err
		}
		buf.Write(enc)
		return nil
	}
}

func personalSignDigest(canonical []byte) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(canonical))
	return crypto.Keccak256([]byte(prefix), canonical)
}

// setSignerClock is a test seam: validity is time-dependent, and a test
// that waits for a window to pass is a test nobody runs.
func setSignerClock(s *Signer, at time.Time) {
	if s != nil {
		s.now = func() time.Time { return at }
	}
}

// CanonicalNonAdmissionPayload canonicalizes a non-admission record with
// the SAME rules a settlement uses — proto field names, JCS ordering —
// so a consumer verifies both with one code path and cannot end up
// trusting one shape while rejecting the other.
func CanonicalNonAdmissionPayload(rec *pb.NonAdmissionRecord) (json.RawMessage, error) {
	if rec == nil {
		return nil, fmt.Errorf("settlement: nil non-admission record")
	}
	raw, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(rec)
	if err != nil {
		return nil, fmt.Errorf("settlement: marshal non-admission record: %w", err)
	}
	return canonicalize(raw)
}

// EncodeNonAdmission signs and wraps a non-admission record in the same
// envelope shape as a settlement.
//
// Unlike a settlement, an UNSIGNED non-admission record is refused
// outright rather than emitted with the signature omitted. A settlement
// still carries useful accounting when a broker holds no delegation; a
// non-admission record's entire purpose is to be evidence somebody can
// be held to, and an unsigned one is an anonymous assertion that a
// refund should happen.
func EncodeNonAdmission(rec *pb.NonAdmissionRecord, signer *Signer) (string, error) {
	if signer == nil {
		return "", fmt.Errorf("settlement: refusing to emit an unsigned non-admission record; " +
			"it is only evidence if it is attributable")
	}
	canonical, err := CanonicalNonAdmissionPayload(rec)
	if err != nil {
		return "", err
	}
	value, err := signer.Sign(canonical)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(Envelope{
		Payload: canonical,
		Signature: &Signature{
			Algorithm:        Alg,
			Canonicalization: Canonicalization,
			Value:            value,
		},
	})
	if err != nil {
		return "", fmt.Errorf("settlement: marshal non-admission envelope: %w", err)
	}
	return base64.StdEncoding.EncodeToString(out), nil
}
