package sessionstore

import (
	"bytes"
	"errors"
	"os"
	"testing"
	"time"
)

func TestRevisionSealedIntentAndAtomicResponseSurviveRestart(t *testing.T) {
	s, path := openTemp(t)
	r := sampleRecord()
	r.AccountAuthorizationID = "predecessor"
	fp := []byte("request-fingerprint")
	lease := time.Now().Add(time.Minute).UTC()
	r.RevisionIntent = &RevisionIntent{RequestID: "revision", AuthorizationBytes: []byte("private-signed-authorization"), PaymentBytes: []byte("private-payment-batch"), Fingerprint: fp, ReservationWei: "100", LeaseExpiresAt: lease}
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, r.RevisionIntent.AuthorizationBytes) || bytes.Contains(raw, r.RevisionIntent.PaymentBytes) {
		t.Fatal("revision secrets stored in plaintext")
	}
	s, err = Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.CommitRevision(r.SessionID, "revision", fp, lease, "900", func(rec *Record) error {
		rec.AccountAuthorizationID = "successor"
		return errors.New("injected failure")
	}); err == nil {
		t.Fatal("injected failure ignored")
	}
	got, err := s.Get(r.SessionID)
	if err != nil || got.RevisionIntent == nil || got.AccountAuthorizationID != "predecessor" {
		t.Fatalf("transaction did not roll back: %+v %v", got, err)
	}
	prior, err := s.TopUpRecall(r.SessionID, "revision", fp)
	if err != nil || prior != nil {
		t.Fatalf("response committed without authority: %+v %v", prior, err)
	}
	if err = s.CommitRevision(r.SessionID, "revision", []byte("wrong"), lease, "900", func(*Record) error { return nil }); err == nil {
		t.Fatal("changed intent accepted")
	}
	if err = s.CommitRevision(r.SessionID, "revision", fp, lease, "900", func(rec *Record) error { rec.AccountAuthorizationID = "successor"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err = s.Get(r.SessionID)
	if err != nil || got.RevisionIntent != nil || got.AccountAuthorizationID != "successor" {
		t.Fatalf("authority commit lost: %+v %v", got, err)
	}
	prior, err = s.TopUpRecall(r.SessionID, "revision", fp)
	if err != nil || prior == nil || prior.BalanceWei != "900" || !prior.LeaseExpiresAt.Equal(lease) {
		t.Fatalf("response commit lost: %+v %v", prior, err)
	}
}
