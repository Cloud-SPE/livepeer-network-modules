package store

import (
	"errors"
	"math/big"
	"sync"
	"testing"
	"time"
)

func wholesaleSeed(id string, payer, payee []byte, max string, expires time.Time) WholesaleAuthorizationSeed {
	return WholesaleAuthorizationSeed{
		ID: id, Fingerprint: []byte("fp-" + id), Payer: payer, Payee: payee,
		RequestID: "request-" + id, Protocol: "paid-job/v1", Capability: "custom:any",
		Offering: "offer", PriceWei: "10", PerUnits: 1, WorkUnit: "widgets",
		MaxDebitWei: max, MaxTotalUnits: 10, ExpiresAt: expires,
	}
}

func fundLegacySession(t *testing.T, st *Store, payer []byte, workID string, amount int64) {
	t.Helper()
	if _, _, err := st.OpenSession(Session{WorkID: workID, Capability: "custom:any", Offering: "offer", PricePerWorkUnitWei: "10", PerUnits: 1, WorkUnit: "widgets"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SealSender(workID, payer); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreditBalance(payer, workID, big.NewInt(amount)); err != nil {
		t.Fatal(err)
	}
}

func TestWholesaleAdmissionSettlementAndReplay(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(1), bytes20(2)
	fundLegacySession(t, st, payer, "generation-a", 1000)
	now := time.Now().UTC()
	seed := wholesaleSeed("a1", payer, payee, "100", now.Add(time.Hour))

	admitted, err := st.AdmitWholesale(seed, "generation-a", nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Transferred.Int64() != 1000 || admitted.Account.Available().Int64() != 900 {
		t.Fatalf("transfer=%s available=%s", admitted.Transferred, admitted.Account.Available())
	}
	legacy, err := st.GetBalance(payer, "generation-a")
	if err != nil || legacy.Sign() != 0 {
		t.Fatalf("legacy balance=%s err=%v", legacy, err)
	}
	replay, err := st.AdmitWholesale(seed, "generation-a", nil, now)
	if err != nil || !replay.Replayed || replay.Account.CreditedWei != "1000" {
		t.Fatalf("admission replay=%+v err=%v", replay, err)
	}

	settled, err := st.SettleWholesale(payer, payee, seed.ID, 4, 1, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if settled.Billed.Int64() != 40 || settled.Released.Int64() != 60 || settled.Account.Available().Int64() != 960 {
		t.Fatalf("billed=%s released=%s available=%s", settled.Billed, settled.Released, settled.Account.Available())
	}
	settledReplay, err := st.SettleWholesale(payer, payee, seed.ID, 4, 1, now.Add(2*time.Minute))
	if err != nil || !settledReplay.Replayed || settledReplay.Billed.Int64() != 40 {
		t.Fatalf("settlement replay=%+v err=%v", settledReplay, err)
	}
	totals, err := st.GetWholesaleTotals()
	if err != nil || totals.CreditedWei != "1000" || totals.ReservedWei != "0" || totals.DebitedWei != "40" || totals.Available().Int64() != 960 {
		t.Fatalf("wholesale totals=%+v available=%v err=%v", totals, totals.Available(), err)
	}
}

func TestWholesaleConcurrentReservationsDoNotOverspend(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(3), bytes20(4)
	fundLegacySession(t, st, payer, "generation", 100)
	now := time.Now().UTC()
	// Move the initial credit and reserve none by settling a zero-cost grant.
	seed := wholesaleSeed("bootstrap", payer, payee, "1", now.Add(time.Hour))
	if _, err := st.AdmitWholesale(seed, "generation", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SettleWholesale(payer, payee, seed.ID, 0, 1, now); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	var successes int
	var mu sync.Mutex
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := st.AdmitWholesale(wholesaleSeed(id, payer, payee, "100", now.Add(time.Hour)), "", nil, now)
			if err == nil {
				mu.Lock()
				successes++
				mu.Unlock()
			} else if !errors.Is(err, ErrInsufficientWholesale) {
				t.Errorf("admit %s: %v", id, err)
			}
		}(id)
	}
	wg.Wait()
	if successes != 1 {
		t.Fatalf("successes=%d want 1", successes)
	}
}

func TestExpiredAuthorizationIsIrrevocable(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(5), bytes20(6)
	now := time.Now().UTC()
	seed := wholesaleSeed("expired", payer, payee, "10", now.Add(-time.Second))
	result, err := st.AdmitWholesale(seed, "", nil, now)
	if err != nil || result.Authorization.State != AuthorizationExpiredUnused {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	result, err = st.AdmitWholesale(seed, "", nil, now.Add(-time.Hour))
	if err != nil || !result.Replayed || result.Authorization.State != AuthorizationExpiredUnused {
		t.Fatalf("revival result=%+v err=%v", result, err)
	}
}

func TestWholesaleSessionAdvancesCumulativeUsageAndReplacesRunway(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(0x31), bytes20(0x41)
	now := time.Now().UTC()
	fundLegacySession(t, st, payer, "generation-session", 1000)
	seed := wholesaleSeed("session-auth", payer, payee, "1000", now.Add(time.Hour))
	seed.SessionID, seed.Protocol, seed.PriceWei, seed.MaxTotalUnits = "session-1", "paid-session/v1", "10", 100
	admitted, err := st.AdmitWholesale(seed, "generation-session", big.NewInt(100), now)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Account.Available().Int64() != 900 || admitted.Authorization.ReservedWei != "100" {
		t.Fatalf("admitted=%+v account=%+v", admitted.Authorization, admitted.Account)
	}
	advanced, err := st.AdvanceWholesale(payer, payee, seed.ID, 4, big.NewInt(100), 1, "", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if advanced.BilledDelta.Int64() != 40 || advanced.Account.Available().Int64() != 860 || advanced.Authorization.ReservedWei != "100" {
		t.Fatalf("advanced=%+v account=%+v", advanced.Authorization, advanced.Account)
	}
	replay, err := st.AdvanceWholesale(payer, payee, seed.ID, 4, big.NewInt(100), 1, "", now.Add(2*time.Minute))
	if err != nil || !replay.Replayed {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	revision := wholesaleSeed("session-auth-v2", payer, payee, "1500", now.Add(2*time.Hour))
	revision.SessionID, revision.Protocol, revision.PriceWei, revision.MaxTotalUnits, revision.Revision, revision.PredecessorID = "session-1", "paid-session/v1", "10", 150, 1, seed.ID
	revised, err := st.AdmitWholesale(revision, "", big.NewInt(100), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if revised.Authorization.ActualUnits != 4 || revised.Authorization.BilledWei != "40" {
		t.Fatalf("revision lost cumulative state: %+v", revised.Authorization)
	}
	pred, _ := st.GetWholesaleAuthorization(payer, seed.ID)
	if pred.State != AuthorizationSuperseded || pred.ReservedWei != "0" {
		t.Fatalf("predecessor=%+v", pred)
	}
	settled, err := st.SettleWholesale(payer, payee, revision.ID, 4, 2, now.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if settled.Billed.Int64() != 40 || settled.Released.Int64() != 100 || settled.Account.Available().Int64() != 960 {
		t.Fatalf("settled=%+v account=%+v", settled, settled.Account)
	}
}

func TestUnderfundedRevisionKeepsPredecessorAdmitted(t *testing.T) {
	st := openTestStore(t)
	payer, payee := bytes20(0x51), bytes20(0x61)
	now := time.Now().UTC()
	fundLegacySession(t, st, payer, "generation-revision", 100)
	seed := wholesaleSeed("session-v1", payer, payee, "100", now.Add(time.Hour))
	seed.SessionID, seed.Protocol, seed.MaxTotalUnits = "session-1", "paid-session/v1", 10
	if _, err := st.AdmitWholesale(seed, "generation-revision", big.NewInt(100), now); err != nil {
		t.Fatal(err)
	}

	revision := wholesaleSeed("session-v2", payer, payee, "200", now.Add(time.Hour))
	revision.SessionID, revision.Protocol, revision.MaxTotalUnits = "session-1", "paid-session/v1", 20
	revision.Revision, revision.PredecessorID = 1, seed.ID
	if _, err := st.AdmitWholesale(revision, "", big.NewInt(200), now.Add(time.Minute)); !errors.Is(err, ErrInsufficientWholesale) {
		t.Fatalf("revision err=%v, want insufficient wholesale", err)
	}
	pred, err := st.GetWholesaleAuthorization(payer, seed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pred.State != AuthorizationAdmitted || pred.ReservedWei != "100" {
		t.Fatalf("underfunded revision changed predecessor: %+v", pred)
	}
	if _, err := st.GetWholesaleAuthorization(payer, revision.ID); !errors.Is(err, ErrAuthorizationNotFound) {
		t.Fatalf("successor lookup err=%v, want not found", err)
	}
}
