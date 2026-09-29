package sessionstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func TestJobRecoverySealedRetainedAndRestarted(t *testing.T) {
	s, path := openTemp(t)
	_, _, err := s.JobBegin("req", []byte("fp"), "job", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("exact-secret-authorization")
	funding := []byte("exact-secret-funding")
	err = s.UpdateJob("req", func(r *JobRecord) error {
		r.Recovery = &JobRecovery{Admission: PendingDebit{AuthorizationBytes: auth, WholesaleAccountID: "account-one"}, PaymentBytes: funding, ExecutionStarted: true}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.EvictJobs(time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	err = s.db.View(func(tx *bolt.Tx) error {
		raw := tx.Bucket([]byte(jobsBucket)).Get([]byte("req"))
		for _, secret := range [][]byte{auth, funding, []byte("account-one")} {
			if bytes.Contains(raw, secret) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString(secret))) {
				t.Fatal("unsealed job secret")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, testKey())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rec, err := reopened.JobByRequestID("req")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State != JobInFlight || !rec.Recovery.ExecutionStarted || !bytes.Equal(rec.Recovery.PaymentBytes, funding) || !bytes.Equal(rec.Recovery.Admission.AuthorizationBytes, auth) {
		t.Fatalf("lost recovery: %+v", rec)
	}
	// Corruption must never turn an unreadable admission into absence.
	rec.RecoverySealed[len(rec.RecoverySealed)-1] ^= 1
	rec.Recovery = nil
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err = reopened.decodeJob(raw, &JobRecord{}); err == nil {
		t.Fatal("accepted corrupted recovery")
	}
}
