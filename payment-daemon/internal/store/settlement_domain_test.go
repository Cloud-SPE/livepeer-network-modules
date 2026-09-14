package store

import (
	"bytes"
	bolt "go.etcd.io/bbolt"
	"math/big"
	"path/filepath"
	"testing"
	"time"
)

func TestSettlementDomainPersistenceAndMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20)
	// Legacy credit remains intact when its ledger acquires an identity.
	if _, _, e := s.OpenSession(Session{WorkID: "legacy", Capability: "work", Offering: "offer", PricePerWorkUnitWei: "1", PerUnits: 1, WorkUnit: "unit"}); e != nil {
		t.Fatal(e)
	}
	if e := s.SealSender("legacy", payer); e != nil {
		t.Fatal(e)
	}
	if _, e := s.CreditBalance(payer, "legacy", big.NewInt(100)); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.FundWholesale(payer, payee, "legacy", time.Now()); e != nil {
		t.Fatal(e)
	}
	before, e := s.GetWholesaleAccount(payer, payee)
	if e != nil {
		t.Fatal(e)
	}
	id, e := s.InitSettlementDomain("", 42161, payee)
	if e != nil || !ValidSettlementDomainID(id) {
		t.Fatalf("%q %v", id, e)
	}
	if e := s.Close(); e != nil {
		t.Fatal(e)
	}
	s, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	again, e := s.InitSettlementDomain("", 42161, payee)
	if e != nil || again != id {
		t.Fatalf("restart %q %v", again, e)
	}
	after, e := s.GetWholesaleAccount(payer, payee)
	if e != nil || after.CreditedWei != before.CreditedWei || after.Version != before.Version {
		t.Fatal("upgrade/reset changed credit or version", e)
	}
	if _, e = s.InitSettlementDomain("0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", 42161, payee); e == nil {
		t.Fatal("accepted conflicting bootstrap ID")
	}
	if _, e = s.InitSettlementDomain("", 1, payee); e == nil {
		t.Fatal("accepted changed chain")
	}
	if _, e = s.InitSettlementDomain("", 42161, payer); e == nil {
		t.Fatal("accepted changed payee")
	}
	// A removed namespace bucket in a migrated ledger must not regenerate.
	if e = s.db.Update(func(tx *bolt.Tx) error { return tx.DeleteBucket([]byte(settlementDomainBucket)) }); e != nil {
		t.Fatal(e)
	}
	if _, e = s.InitSettlementDomain("", 42161, payee); e == nil {
		t.Fatal("regenerated lost financial identity")
	}
}

func TestSettlementDomainBootstrap(t *testing.T) {
	s, e := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	id := "0xbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if _, e = s.InitSettlementDomain("not-an-id", 1, bytes.Repeat([]byte{2}, 20)); e == nil {
		t.Fatal("accepted malformed import")
	}
	got, e := s.InitSettlementDomain(id, 1, bytes.Repeat([]byte{2}, 20))
	if e != nil || got != id {
		t.Fatalf("import %s %v", got, e)
	}
}

func TestSettlementDomainUpgradeRequiresDrain(t *testing.T) {
	s := openTestStore(t)
	payer, payee := bytes20(1), bytes20(2)
	fundLegacySession(t, s, payer, "generation", 1000)
	now := time.Now().UTC()
	seed := wholesaleSeed("active", payer, payee, "100", now.Add(time.Hour))
	if _, err := s.AdmitWholesale(seed, "generation", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InitSettlementDomain("", 42161, payee); err == nil {
		t.Fatal("upgraded with admitted legacy authorization")
	}
	if _, err := s.SettleWholesale(payer, payee, "active", 2, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InitSettlementDomain("", 42161, payee); err != nil {
		t.Fatal(err)
	}
	a, err := s.GetWholesaleAccount(payer, payee)
	if err != nil || a.Available().Int64() != 980 {
		t.Fatalf("drain lost money: %+v %v", a, err)
	}
}
