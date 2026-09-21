package store

import (
	"bytes"
	"math/big"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIsolatedFundingRestartRollbackAndLegacyRetention(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20)
	now := time.Now().UTC()
	// Existing wallet-wide credit is retained and cannot be selected by a name.
	_, _, err = s.OpenSession(Session{WorkID: "legacy", Sender: payer, Recipient: payee, BalanceWei: "0"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SealSender("legacy", payer); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreditBalance(payer, "legacy", big.NewInt(71)); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.FundWholesale(payer, payee, "legacy", now); err != nil {
		t.Fatal(err)
	}
	key := TicketSessionKey{Sender: payer, Recipient: payee, Capability: "c", Offering: "o", WholesaleAccountID: "loc-prod", TicketStreamID: "stream-a"}
	_, _, err = s.GetOrCreateTicketSession(key, Session{WorkID: "new", RecipientRand: "123"})
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 64)
	first, _, err := s.ApplyWholesaleFunding(payer, payee, "loc-prod", "new", id, []FundingTicket{{Nonce: 1, Credit: big.NewInt(11)}}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Failure on a later ticket must roll back all earlier nonce writes and credit.
	_, _, err = s.ApplyWholesaleFunding(payer, payee, "loc-prod", "new", strings.Repeat("b", 64), []FundingTicket{{Nonce: 2, Credit: big.NewInt(7)}, {Nonce: 1, Credit: big.NewInt(7)}}, now)
	if err != ErrNonceAlreadySeen {
		t.Fatalf("want nonce failure, got %v", err)
	}
	if seen, err := s.NonceSeen(big.NewInt(123), 2); err != nil || seen {
		t.Fatal("failed funding partially consumed nonce")
	}
	stream, err := s.TicketStreamID()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	same, err := s.TicketStreamID()
	if err != nil || same != stream {
		t.Fatal("restart changed stream identity")
	}
	replay, err := s.GetFundingReceipt(payer, payee, "loc-prod", id)
	if err != nil || !reflect.DeepEqual(first, replay) {
		t.Fatalf("receipt restart mismatch: %v", err)
	}
	named, _ := s.GetWholesaleAccount(payer, payee, "loc-prod")
	legacy, _ := s.GetWholesaleAccount(payer, payee)
	if named.CreditedWei != "11" || legacy.CreditedWei != "71" {
		t.Fatalf("named=%s legacy=%s", named.CreditedWei, legacy.CreditedWei)
	}
	if _, _, err = s.FundWholesale(payer, payee, "legacy", now, "loc-prod"); err == nil {
		t.Fatal("legacy balance migration silently accepted")
	}
	if bytes.Equal(wholesaleAuthorizationKey(payer, "x\x00loc-prod"), wholesaleAuthorizationKey(payer, "x", "loc-prod")) {
		t.Fatal("legacy and scoped authorization keys collide")
	}
	// Closing an isolated generation must not delete a legacy active index.
	legacyKey := TicketSessionKey{Sender: payer, Recipient: payee, Capability: "c", Offering: "o"}
	_, _, err = s.GetOrCreateTicketSession(legacyKey, Session{WorkID: "legacy-active", RecipientRand: "777"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CloseSession(payer, "new"); err != nil {
		t.Fatal(err)
	}
	if found, err := s.TicketSessionFor(legacyKey); err != nil || found.WorkID != "legacy-active" {
		t.Fatal("isolated close removed legacy index")
	}
}
