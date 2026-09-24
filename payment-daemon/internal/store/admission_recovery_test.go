package store

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCancelAdmissionAtomicWithAdmissionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receiver.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "float", 1000)
	bootstrap := wholesaleSeed("bootstrap", payer, payee, "1000", now.Add(time.Hour))
	if _, err = st.AdmitWholesale(bootstrap, "float", big.NewInt(1), now); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SettleWholesale(payer, payee, "bootstrap", 0, 1, now); err != nil {
		t.Fatal(err)
	}
	var canceled []WholesaleAuthorizationSeed
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("racing-%d", i)
		seed := wholesaleSeed(id, payer, payee, "10", now.Add(time.Hour))
		fp := sha256.Sum256([]byte(id))
		seed.Fingerprint = fp[:]
		var wg sync.WaitGroup
		var admitErr, cancelErr error
		var result *WholesaleAdmissionResult
		var fenced bool
		wg.Add(2)
		go func() { defer wg.Done(); _, admitErr = st.AdmitWholesale(seed, "", big.NewInt(10), now) }()
		go func() {
			defer wg.Done()
			result, fenced, cancelErr = st.CancelAuthorizationAdmission(payer, payee, id, fp[:])
		}()
		wg.Wait()
		if cancelErr != nil {
			t.Fatal(cancelErr)
		}
		if fenced {
			if !errors.Is(admitErr, ErrAuthorizationCanceled) {
				t.Fatalf("cancellation lost race: %v", admitErr)
			}
			canceled = append(canceled, seed)
		} else {
			if admitErr != nil || result.Authorization == nil || result.Authorization.State != AuthorizationAdmitted {
				t.Fatalf("accepted authority lost: %v %+v", admitErr, result)
			}
			if _, err := st.SettleWholesale(payer, payee, id, 0, 1, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	// Always cover a fenced identity across restart, regardless of race ordering.
	seed := wholesaleSeed("canceled", payer, payee, "10", now.Add(time.Hour))
	fp := sha256.Sum256([]byte(seed.ID))
	seed.Fingerprint = fp[:]
	if _, fenced, err := st.CancelAuthorizationAdmission(payer, payee, seed.ID, fp[:]); err != nil || !fenced {
		t.Fatalf("fence: %v %v", fenced, err)
	}
	canceled = append(canceled, seed)
	if err = st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, seed := range canceled {
		if _, err := st.AdmitWholesale(seed, "", big.NewInt(10), now); !errors.Is(err, ErrAuthorizationCanceled) {
			t.Fatalf("fence lost after restart: %v", err)
		}
		changed := sha256.Sum256([]byte("changed"))
		if _, _, err := st.CancelAuthorizationAdmission(payer, payee, seed.ID, changed[:]); !errors.Is(err, ErrAuthorizationFingerprint) {
			t.Fatalf("changed cancellation: %v", err)
		}
	}
	account, err := st.GetWholesaleAccount(payer, payee)
	if err != nil || account.ReservedWei != "0" || account.DebitedWei != "0" {
		t.Fatalf("cancellation changed accounting: %+v %v", account, err)
	}
}

func TestCancelAcceptedRevisionPreservesInheritedBilling(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "float", 1000)
	previous := wholesaleSeed("previous", payer, payee, "120", now.Add(time.Hour))
	previous.Protocol, previous.SessionID, previous.PriceWei, previous.MaxTotalUnits = "paid-session/v1", "session", "1", 120
	if _, err := st.AdmitWholesale(previous, "float", big.NewInt(120), now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceWholesale(payer, payee, previous.ID, 74, big.NewInt(46), 1, "", now); err != nil {
		t.Fatal(err)
	}
	next := previous
	next.ID, next.PredecessorID, next.MaxDebitWei, next.MaxTotalUnits, next.Revision = "next", previous.ID, "180", 180, 1
	fp := sha256.Sum256([]byte("signed successor"))
	next.Fingerprint = fp[:]
	if _, err := st.AdmitWholesale(next, "", big.NewInt(120), now); !errors.Is(err, ErrReservationExceedsRemaining) {
		t.Fatalf("oversize: %v", err)
	}
	if _, err := st.AdmitWholesale(next, "", big.NewInt(106), now); err != nil {
		t.Fatal(err)
	}
	result, canceled, err := st.CancelAuthorizationAdmission(payer, payee, next.ID, fp[:])
	if err != nil || canceled || result.Authorization.ActualUnits != 74 || result.Authorization.BilledWei != "74" || result.Authorization.ReservedWei != "106" || result.Authorization.State != AuthorizationAdmitted {
		t.Fatalf("accepted revision altered: %+v canceled=%v err=%v", result, canceled, err)
	}
	if _, err := st.SettleWholesale(payer, payee, next.ID, 74, 2, now); err != nil {
		t.Fatal(err)
	}
	result, canceled, err = st.CancelAuthorizationAdmission(payer, payee, next.ID, fp[:])
	if err != nil || canceled || result.Authorization.State != AuthorizationSettled || result.Authorization.BilledWei != "74" {
		t.Fatal("settled revision altered", err)
	}
}
