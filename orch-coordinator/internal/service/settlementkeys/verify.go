// Package settlementkeys discovers, proves and merges the hot keys the
// manifest delegates settlement signing to.
//
// The operator used to copy a 130-character public key out of each
// broker's startup log into coordinator-config by hand, and a wrong
// paste passed validation and failed every settlement as
// missing_delegation. Now a broker announces its key at
// GET /registry/settlement-keys with a proof of possession, the
// coordinator carries proven keys into the candidate, and the cold key
// still decides: secure-orch holds any delegation change for a human.
//
// Trust boundary, stated once: this package proves that the server
// answering at a broker's URL holds the private half of the key it
// names, and that the statement was made for this orchestrator and
// this URL. It does not, and cannot, prove the coordinator itself is
// honest — that is what the cold key's review is for.
package settlementkeys

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/verify"
	"github.com/Cloud-SPE/livepeer-network-modules/orch-coordinator/internal/types"
)

// Discovered is one announced key after verification.
type Discovered struct {
	PublicKey string
	// NotBefore / ExpiresAt are the window the broker declared; zero
	// when it signs unbounded and the coordinator assigns one.
	NotBefore time.Time
	ExpiresAt time.Time
	BaseURL   string
	IssuedAt  time.Time
	// Proven is true when the signature recovers to PublicKey and the
	// statement names this orchestrator and this broker. Reason says
	// why not.
	Proven bool
	Reason string
}

// Errors a caller can switch on.
var (
	ErrUnsigned        = errors.New("settlementkeys: announcement is unsigned")
	ErrScheme          = errors.New("settlementkeys: unsupported signature scheme")
	ErrMalformedKey    = errors.New("settlementkeys: public_key is not an uncompressed secp256k1 key")
	ErrMalformedSig    = errors.New("settlementkeys: signature malformed")
	ErrUnproven        = errors.New("settlementkeys: signature does not recover to public_key")
	ErrWrongOrch       = errors.New("settlementkeys: statement is for a different orchestrator")
	ErrWrongBaseURL    = errors.New("settlementkeys: statement names a different broker URL")
	ErrMalformedWindow = errors.New("settlementkeys: validity window malformed")
)

// Verify checks one announcement against the orchestrator and broker
// URL the coordinator expected to be talking to. The returned
// Discovered is populated even on failure so the operator can see what
// the broker claimed; Proven is what the merge acts on.
func Verify(a types.BrokerSettlementAnnouncement, expectOrch, expectBaseURL string) Discovered {
	d := Discovered{PublicKey: strings.ToLower(strings.TrimSpace(a.Statement.PublicKey)), BaseURL: a.Statement.BaseURL}
	if t, err := time.Parse(time.RFC3339, a.Statement.IssuedAt); err == nil {
		d.IssuedAt = t.UTC()
	}
	err := verifyAnnouncement(a, expectOrch, expectBaseURL, &d)
	if err != nil {
		d.Reason = err.Error()
		return d
	}
	d.Proven = true
	return d
}

func verifyAnnouncement(a types.BrokerSettlementAnnouncement, expectOrch, expectBaseURL string, d *Discovered) error {
	st := a.Statement
	if !validPublicKey(d.PublicKey) {
		return ErrMalformedKey
	}
	if a.Signature == nil {
		if a.Error != "" {
			return fmt.Errorf("%w: broker reports %q", ErrUnsigned, a.Error)
		}
		return ErrUnsigned
	}
	if a.Signature.Algorithm != "secp256k1" || a.Signature.Canonicalization != "jcs" {
		return fmt.Errorf("%w: %s/%s", ErrScheme, a.Signature.Algorithm, a.Signature.Canonicalization)
	}
	var err error
	if d.NotBefore, err = parseOptionalTime(st.NotBefore); err != nil {
		return fmt.Errorf("%w: not_before: %v", ErrMalformedWindow, err)
	}
	if d.ExpiresAt, err = parseOptionalTime(st.ExpiresAt); err != nil {
		return fmt.Errorf("%w: expires_at: %v", ErrMalformedWindow, err)
	}
	if !d.NotBefore.IsZero() && !d.ExpiresAt.IsZero() && !d.ExpiresAt.After(d.NotBefore) {
		return fmt.Errorf("%w: expires_at is not after not_before", ErrMalformedWindow)
	}

	// The proof first: what follows compares fields the signature
	// covers, and a mismatch on unsigned bytes would say nothing.
	canonical, err := canonicalStatement(st)
	if err != nil {
		return err
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimPrefix(a.Signature.Value, "0x"), "0X"))
	if err != nil || len(sig) != 65 {
		return ErrMalformedSig
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	pub, err := crypto.SigToPub(verify.PersonalSignDigest(canonical), sig)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedSig, err)
	}
	if recovered := "0x" + hex.EncodeToString(crypto.FromECDSAPub(pub)); recovered != d.PublicKey {
		return ErrUnproven
	}

	if !strings.EqualFold(strings.TrimSpace(st.OrchEthAddress), strings.TrimSpace(expectOrch)) {
		return fmt.Errorf("%w: statement says %s", ErrWrongOrch, st.OrchEthAddress)
	}
	if st.BaseURL != "" && !sameBaseURL(st.BaseURL, expectBaseURL) {
		return fmt.Errorf("%w: statement says %s, scraped %s", ErrWrongBaseURL, st.BaseURL, expectBaseURL)
	}
	return nil
}

// canonicalStatement is the JCS form of the statement: keys sorted, no
// whitespace, strings encoded by encoding/json — the same bytes the
// broker signed. The struct's field order IS the sorted key order, and
// omitempty matches the broker's, so json.Marshal yields JCS directly.
func canonicalStatement(st types.BrokerSettlementStatement) ([]byte, error) {
	ordered := struct {
		BaseURL        string `json:"base_url,omitempty"`
		ExpiresAt      string `json:"expires_at,omitempty"`
		IssuedAt       string `json:"issued_at"`
		NotBefore      string `json:"not_before,omitempty"`
		OrchEthAddress string `json:"orch_eth_address"`
		PublicKey      string `json:"public_key"`
	}{st.BaseURL, st.ExpiresAt, st.IssuedAt, st.NotBefore, st.OrchEthAddress, st.PublicKey}
	b, err := json.Marshal(ordered)
	if err != nil {
		return nil, fmt.Errorf("settlementkeys: canonical statement: %w", err)
	}
	return b, nil
}

func validPublicKey(s string) bool {
	if len(s) != 132 || !strings.HasPrefix(s, "0x04") {
		return false
	}
	_, err := hex.DecodeString(s[2:])
	return err == nil
}

func parseOptionalTime(s string) (time.Time, error) {
	if strings.TrimSpace(s) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// sameBaseURL compares two base URLs the way the config normalizes
// them: scheme and host case-insensitively, path without a trailing
// slash.
func sameBaseURL(a, b string) bool {
	norm := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimRight(s, "/")
		if i := strings.Index(s, "://"); i > 0 {
			rest := s[i+3:]
			host, path := rest, ""
			if j := strings.Index(rest, "/"); j >= 0 {
				host, path = rest[:j], rest[j:]
			}
			return strings.ToLower(s[:i]) + "://" + strings.ToLower(host) + path
		}
		return s
	}
	return norm(a) == norm(b)
}

// Fingerprint is the short form an operator compares by eye: enough of
// a 65-byte key to tell two apart, the full value one click away.
func Fingerprint(publicKey string) string {
	if len(publicKey) <= 16 {
		return publicKey
	}
	return publicKey[:10] + "…" + publicKey[len(publicKey)-6:]
}
