package settlement

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/crypto"
)

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settlement.key")
	if err := os.WriteFile(path, []byte("0x"+hex.EncodeToString(crypto.FromECDSA(key))+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestAnnounce_ProvesPossession(t *testing.T) {
	s := newTestSigner(t)
	s.SetValidity(time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC), time.Date(2027, 9, 7, 0, 0, 0, 0, time.UTC))
	setSignerClock(s, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))

	a := s.Announce("0xD00354656922168815FCD1E51CBDDB9E359E3C7F", "https://ai2-rig-broker.xode.app/")
	if a.Error != "" || a.Signature == nil {
		t.Fatalf("announce failed: %+v", a)
	}
	st := a.Statement
	if st.OrchEthAddress != "0xd00354656922168815fcd1e51cbddb9e359e3c7f" {
		t.Fatalf("orch address not normalized: %s", st.OrchEthAddress)
	}
	if st.PublicKey != s.PublicKeyHex() || !strings.HasPrefix(st.PublicKey, "0x04") || len(st.PublicKey) != 132 {
		t.Fatalf("public_key = %s", st.PublicKey)
	}
	if st.NotBefore != "2026-09-07T00:00:00Z" || st.ExpiresAt != "2027-09-07T00:00:00Z" || st.IssuedAt != "2026-09-08T12:00:00Z" {
		t.Fatalf("window/issued: %+v", st)
	}
	if err := VerifyAnnouncement(a); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

// Possession is proven regardless of the window: a key outside its
// window is the one the operator most needs to see.
func TestAnnounce_SignsOutsideValidity(t *testing.T) {
	s := newTestSigner(t)
	s.SetValidity(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
	setSignerClock(s, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if _, err := s.Sign([]byte(`{}`)); !errors.Is(err, ErrKeyOutsideValidity) {
		t.Fatalf("records outside the window must refuse: %v", err)
	}
	a := s.Announce("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "")
	if err := VerifyAnnouncement(a); err != nil {
		t.Fatalf("announcement outside the window should still prove possession: %v", err)
	}
	if a.Statement.BaseURL != "" {
		t.Fatalf("base_url should be omitted when unset, got %q", a.Statement.BaseURL)
	}
}

func TestVerifyAnnouncement_RejectsTamperAndWrongKey(t *testing.T) {
	s := newTestSigner(t)
	a := s.Announce("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "https://a.example")

	tampered := a
	tampered.Statement.OrchEthAddress = "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := VerifyAnnouncement(tampered); !errors.Is(err, ErrAnnouncementUnproven) {
		t.Fatalf("tampered statement verified: %v", err)
	}

	other := newTestSigner(t)
	swapped := a
	swapped.Statement.PublicKey = other.PublicKeyHex()
	if err := VerifyAnnouncement(swapped); !errors.Is(err, ErrAnnouncementUnproven) {
		t.Fatalf("claiming another key verified: %v", err)
	}

	unsigned := Announcement{Statement: a.Statement, Error: "no signing key"}
	if err := VerifyAnnouncement(unsigned); err == nil || !strings.Contains(err.Error(), "no signing key") {
		t.Fatalf("unsigned announcement verified: %v", err)
	}
}

// The canonical statement is JCS as both ends of this repo write it:
// keys sorted, no whitespace, strings encoded by encoding/json (so `&`
// is \u0026 — the coordinator's CanonicalBytes does the same, and the
// two must agree byte for byte for the proof to verify there).
func TestCanonicalStatement_IsJCS(t *testing.T) {
	got, err := CanonicalStatement(Statement{
		OrchEthAddress: "0xaa", PublicKey: "0x04bb", BaseURL: "https://a.example/x?y=1&z=2", IssuedAt: "2026-09-08T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"base_url":"https://a.example/x?y=1\u0026z=2","issued_at":"2026-09-08T12:00:00Z","orch_eth_address":"0xaa","public_key":"0x04bb"}`
	if string(got) != want {
		t.Fatalf("canonical:\n got %s\nwant %s", got, want)
	}
}
