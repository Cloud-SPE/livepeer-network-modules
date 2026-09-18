package settlement

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

// Announcement is the broker's public statement of the key it signs
// settlements with, served at GET /registry/settlement-keys so the
// coordinator can discover it instead of an operator copying 130 hex
// characters out of a log line.
//
// The statement is signed by the key it names. That is a proof of
// possession, not of identity: it shows the server answering at the
// broker's URL holds the private half, so a coordinator never delegates
// to a key that was pasted wrong, and a key nobody holds cannot be
// smuggled into a manifest through this path. Which broker the URL
// belongs to is the transport's job (TLS) and the cold key's
// (secure-orch holds every delegation change for a human).
type Announcement struct {
	Statement Statement  `json:"statement"`
	Signature *Signature `json:"signature,omitempty"`
	// Error is set instead of Signature when the key could not sign the
	// statement; the coordinator treats such a key as unproven.
	Error string `json:"error,omitempty"`
}

// Statement is what the key signs. Everything a coordinator needs to
// bind the key to this orchestrator and this broker is inside the
// signed bytes, so the proof cannot be replayed into another
// operator's manifest.
type Statement struct {
	OrchEthAddress string `json:"orch_eth_address"`
	PublicKey      string `json:"public_key"`
	// BaseURL is the broker's external_base_url when it has one.
	BaseURL string `json:"base_url,omitempty"`
	// NotBefore / ExpiresAt are the window the broker was configured
	// with (RFC 3339); absent means the broker signs unbounded and
	// the coordinator applies its own default window.
	NotBefore string `json:"not_before,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	IssuedAt  string `json:"issued_at"`
}

// Announce signs a fresh statement for this key. Validity is not
// enforced here: a proof of possession is about possession, and a key
// outside its window is exactly the one an operator needs to see so
// they can publish a new window.
func (s *Signer) Announce(orchEthAddress, baseURL string) Announcement {
	st := Statement{
		OrchEthAddress: strings.ToLower(strings.TrimSpace(orchEthAddress)),
		PublicKey:      s.PublicKeyHex(),
		BaseURL:        strings.TrimSpace(baseURL),
		IssuedAt:       s.clock().UTC().Format(time.RFC3339),
	}
	if !s.notBefore.IsZero() {
		st.NotBefore = s.notBefore.UTC().Format(time.RFC3339)
	}
	if !s.expiresAt.IsZero() {
		st.ExpiresAt = s.expiresAt.UTC().Format(time.RFC3339)
	}
	out := Announcement{Statement: st}
	canonical, err := CanonicalStatement(st)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	sig, err := s.signRaw(canonical)
	if err != nil {
		out.Error = err.Error()
		return out
	}
	out.Signature = &Signature{Algorithm: Alg, Canonicalization: Canonicalization, Value: sig}
	return out
}

// CanonicalStatement is the JCS form of a statement — the bytes signed.
func CanonicalStatement(st Statement) ([]byte, error) {
	raw, err := marshalStatement(st)
	if err != nil {
		return nil, err
	}
	return canonicalize(raw)
}

// ErrAnnouncementUnproven is returned when a statement's signature does
// not recover to the key it names.
var ErrAnnouncementUnproven = errors.New("settlement: announcement signature does not recover to public_key")

// VerifyAnnouncement checks the proof of possession: the signature over
// the canonical statement must recover to statement.public_key.
func VerifyAnnouncement(a Announcement) error {
	if a.Signature == nil {
		if a.Error != "" {
			return fmt.Errorf("settlement: announcement unsigned: %s", a.Error)
		}
		return errors.New("settlement: announcement unsigned")
	}
	if a.Signature.Algorithm != Alg || a.Signature.Canonicalization != Canonicalization {
		return fmt.Errorf("settlement: announcement scheme %s/%s unsupported", a.Signature.Algorithm, a.Signature.Canonicalization)
	}
	canonical, err := CanonicalStatement(a.Statement)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(a.Signature.Value, "0x"), "0X"))
	if err != nil || len(sig) != 65 {
		return errors.New("settlement: announcement signature malformed")
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(personalSignDigest(canonical), sig)
	if err != nil {
		return fmt.Errorf("settlement: announcement recover: %w", err)
	}
	recovered := "0x" + hex.EncodeToString(crypto.FromECDSAPub(pub))
	if !strings.EqualFold(recovered, a.Statement.PublicKey) {
		return ErrAnnouncementUnproven
	}
	return nil
}

// signRaw signs canonical bytes without the validity gate Sign applies
// to settlement records.
func (s *Signer) signRaw(canonical []byte) (string, error) {
	if s == nil || s.key == nil {
		return "", fmt.Errorf("settlement: no signing key")
	}
	sig, err := crypto.Sign(personalSignDigest(canonical), s.key)
	if err != nil {
		return "", fmt.Errorf("settlement: sign: %w", err)
	}
	if len(sig) == 65 && sig[64] < 27 {
		sig[64] += 27
	}
	return "0x" + hex.EncodeToString(sig), nil
}

func marshalStatement(st Statement) ([]byte, error) {
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, fmt.Errorf("settlement: statement: %w", err)
	}
	return raw, nil
}
