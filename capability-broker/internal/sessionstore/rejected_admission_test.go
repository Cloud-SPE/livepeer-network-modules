package sessionstore

import (
	"bytes"
	"testing"
	"time"
)

func TestRejectedProofSurvivesRestartAndCannotReplaceExecution(t *testing.T) {
	s, path := openTemp(t)
	if _, _, err := s.JobBegin("r", []byte("fp"), "job", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.JobFinishPaymentRejected("r", 402, []byte("digest"), []byte("auth")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordRejectedNonAdmission("r", []byte("wrong"), "proof", time.Now()); err == nil {
		t.Fatal("scope mismatch accepted")
	}
	s.Close()
	s, err := Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	rec, err := s.JobByRequestID("r")
	if err != nil || rec.State != JobPaymentRejected || !bytes.Equal(rec.RejectedAuthorization, []byte("auth")) {
		t.Fatal("lost durable rejection")
	}
	if _, err := s.RecordRejectedNonAdmission("r", []byte("auth"), "proof", time.Now()); err != nil {
		t.Fatal(err)
	}
	existing, err := s.RecordRejectedNonAdmission("r", []byte("auth"), "different-proof", time.Now())
	if err != nil || existing != "proof" {
		t.Fatal("proof not idempotent")
	}
	if _, _, err := s.JobBegin("executed", []byte("fp"), "job2", time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.JobFinish("executed", 200, 10, "tokens", nil, "signed-settlement"); err != nil {
		t.Fatal(err)
	}
	if err := s.JobFinishPaymentRejected("executed", 402, nil, []byte("auth")); err == nil {
		t.Fatal("execution overwritten")
	}
	if _, err := s.RecordRejectedNonAdmission("executed", []byte("auth"), "proof", time.Now()); err == nil {
		t.Fatal("executed work refunded")
	}
}
