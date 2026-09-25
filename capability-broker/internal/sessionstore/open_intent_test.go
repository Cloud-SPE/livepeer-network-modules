package sessionstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

func TestOpenAdmissionIntentSealedAndRestarted(t *testing.T) {
	s, path := openTemp(t)
	if err := s.ReserveOpen("request-one", []byte("fp")); err != nil {
		t.Fatal(err)
	}
	secret := []byte("signed-authority-secret-must-not-be-plaintext")
	if err := s.UpdateReservation("request-one", func(r *OpenReservation) error {
		r.AdmissionIntent = &OpenAdmissionIntent{AuthorizationBytes: secret, Record: *sampleRecord()}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, secret) || bytes.Contains(raw, []byte("rt_topsecret")) {
		t.Fatal("plaintext recovery authority in store")
	}
	_ = s.Close()
	s, err = Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	r, err := s.Reservation("request-one")
	if err != nil || r.AdmissionIntent == nil || !bytes.Equal(r.AdmissionIntent.AuthorizationBytes, secret) || r.AdmissionIntent.Record.SessionID != "sess_1" {
		t.Fatalf("restart=%+v %v", r, err)
	}
	sealed, err := s.sealReservation(&r)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{"identity", "truncated", "corrupt"} {
		t.Run(mutation, func(t *testing.T) {
			var changed OpenReservation
			if err := json.Unmarshal(sealed, &changed); err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "identity":
				changed.RequestID = "request-two"
			case "truncated":
				changed.AdmissionIntentSealed = []byte{1}
			case "corrupt":
				changed.AdmissionIntentSealed[len(changed.AdmissionIntentSealed)-1] ^= 1
			}
			raw, _ := json.Marshal(changed)
			if _, err := s.unsealReservation(raw); err == nil {
				t.Fatal("accepted invalid sealed intent")
			}
		})
	}
}
func TestNonAdmissionRequiresDurableOpenCancellation(t *testing.T) {
	s, _ := openTemp(t)
	if err := s.ReserveOpen("request", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateReservation("request", func(r *OpenReservation) error {
		r.AdmissionIntent = &OpenAdmissionIntent{AuthorizationBytes: []byte("auth")}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordNonAdmission("request", "envelope", time.Now()); !errors.Is(err, ErrExists) {
		t.Fatalf("pending proof=%v", err)
	}
	if err := s.UpdateReservation("request", func(r *OpenReservation) error { r.AdmissionCanceled = true; return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordNonAdmission("request", "envelope", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := s.ReserveOpen("request", nil); err == nil {
		t.Fatal("canceled request re-admitted")
	}
}
