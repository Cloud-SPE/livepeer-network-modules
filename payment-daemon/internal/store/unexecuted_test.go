package store

import (
	"math/big"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestUnexecutedFenceWinsLateAdmissionRaceAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receiver.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "fund", 1000)
	// Move credit once, then race admission against no-execution recovery.
	bootstrap := wholesaleSeed("bootstrap", payer, payee, "100", now.Add(time.Hour))
	if _, err = st.AdmitWholesale(bootstrap, "fund", big.NewInt(1), now); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SettleWholesale(payer, payee, "bootstrap", 0, 1, now); err != nil {
		t.Fatal(err)
	}
	seed := wholesaleSeed("orphan", payer, payee, "100", now.Add(time.Hour))
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() {
		defer wg.Done()
		_, err := st.AdmitWholesale(seed, "", big.NewInt(50), now)
		if err != nil && err != ErrAuthorizationState {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if err := st.CloseUnexecutedAuthorization(payer, payee, "orphan", "no runner binding"); err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if count, err := st.ActiveAuthorizations(); err != nil || count != 0 {
		t.Fatalf("orphan remained %d %v", count, err)
	}
	account, err := st.GetWholesaleAccount(payer, payee)
	if err != nil || account.ReservedWei != "0" || account.DebitedWei != "0" || account.Available().Int64() != 1000 {
		t.Fatalf("account changed %+v %v", account, err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err = st.AdmitWholesale(seed, "", big.NewInt(50), now); err != ErrAuthorizationState {
		t.Fatalf("late admission escaped fence: %v", err)
	}
	if err = st.CloseUnexecutedAuthorization(payer, payee, "orphan", "retry"); err != nil {
		t.Fatal(err)
	}
}
func TestUnexecutedRecoveryCannotDiscardAcceptedUsage(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "fund", 1000)
	seed := wholesaleSeed("bound", payer, payee, "100", now.Add(time.Hour))
	if _, err := st.AdmitWholesale(seed, "fund", big.NewInt(100), now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdvanceWholesale(payer, payee, "bound", 2, big.NewInt(50), 1, "", now); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseUnexecutedAuthorization(payer, payee, "bound", "incorrect unbound assertion"); err == nil {
		t.Fatal("billed work erased")
	}
	auth, err := st.GetWholesaleAuthorization(payer, "bound")
	if err != nil || auth.State != AuthorizationAdmitted || auth.BilledWei != "20" {
		t.Fatalf("work changed %+v %v", auth, err)
	}
}

func TestUnexecutedRecoveryReleasesOnlyUnusedReservationOnce(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "fund", 1000)
	seed := wholesaleSeed("orphan", payer, payee, "100", now.Add(time.Hour))
	if _, err := st.AdmitWholesale(seed, "fund", big.NewInt(50), now); err != nil {
		t.Fatal(err)
	}
	if err := st.CloseUnexecutedAuthorization(payer, bytes20(3), "orphan", "wrong payee"); err == nil {
		t.Fatal("foreign reservation released")
	}
	if err := st.CloseUnexecutedAuthorization(payer, payee, "orphan", "durable no-binding recovery"); err != nil {
		t.Fatal(err)
	}
	auth, err := st.GetWholesaleAuthorization(payer, "orphan")
	if err != nil || auth.State != AuthorizationSettled || auth.ReservedWei != "0" || auth.ReleasedWei != "50" || auth.BilledWei != "0" {
		t.Fatalf("incorrect recovery %+v %v", auth, err)
	}
	account, _ := st.GetWholesaleAccount(payer, payee)
	if account.Available().Int64() != 1000 || account.CreditedWei != "1000" || account.DebitedWei != "0" {
		t.Fatalf("credit/billing changed %+v", account)
	}
	if err = st.CloseUnexecutedAuthorization(payer, payee, "orphan", "retry"); err != nil {
		t.Fatal(err)
	}
	after, _ := st.GetWholesaleAccount(payer, payee)
	if after.Version != account.Version {
		t.Fatal("recovery replay mutated account")
	}
	if err = st.CloseUnexecutedAuthorization(nil, payee, "orphan", "bad identity"); err == nil {
		t.Fatal("invalid identity accepted")
	}
}
