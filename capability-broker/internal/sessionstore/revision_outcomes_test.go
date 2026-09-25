package sessionstore

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestRevisionAndNonAdmissionSerialize(t *testing.T) {
	s, _ := openTemp(t)
	for n := 0; n < 30; n++ {
		r := sampleRecord()
		r.SessionID = fmt.Sprintf("session-%d", n)
		r.GatewaySessionID = fmt.Sprintf("gateway-%d", n)
		if err := s.Create(r); err != nil {
			t.Fatal(err)
		}
		requestID := fmt.Sprintf("refill-%d", n)
		var beginErr, evidenceErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			beginErr = s.BeginRevision(r.SessionID, &RevisionIntent{RequestID: requestID})
		}()
		go func() { defer wg.Done(); _, evidenceErr = s.RecordNonAdmission(requestID, "signed", time.Now()) }()
		wg.Wait()
		if (beginErr == nil) == (evidenceErr == nil) {
			t.Fatalf("exactly one must win: %v / %v", beginErr, evidenceErr)
		}
		if beginErr != nil && !errors.Is(beginErr, ErrNonAdmissionIssued) {
			t.Fatal(beginErr)
		}
		if evidenceErr != nil && !errors.Is(evidenceErr, ErrExists) {
			t.Fatal(evidenceErr)
		}
	}
}

func TestRevisionCoverageSurvivesOldRecordMigrationAndEviction(t *testing.T) {
	s, path := openTemp(t)
	r := sampleRecord()
	r.RevisionIntent = &RevisionIntent{RequestID: "pending-old"}
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	if err := s.TopUpRecord(r.SessionID, "accepted-old", []byte("fingerprint"), time.Now(), "10"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.EvictTopUps(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, requestID := range []string{"pending-old", "accepted-old"} {
		if _, err := s.RecordNonAdmission(requestID, "false-proof", time.Now()); !errors.Is(err, ErrExists) {
			t.Fatalf("%s: %v", requestID, err)
		}
	}
}
