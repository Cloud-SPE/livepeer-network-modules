package store

import (
	"math/big"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvanceReplayAfterRestartExpiryAndLaterSettlement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receiver.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now().UTC()
	fundLegacySession(t, st, payer, "funding", 1000)
	seed := wholesaleSeed("session", payer, payee, "100", now.Add(time.Minute))
	seed.Protocol = "paid-session/v1"
	if _, err = st.AdmitWholesale(seed, "funding", big.NewInt(50), now); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceWholesale(payer, payee, "session", 2, big.NewInt(50), 1, "", now); err != nil {
		t.Fatal(err)
	}
	if _, err = st.AdvanceWholesale(payer, payee, "session", 4, big.NewInt(30), 2, "", now); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	expired := now.Add(time.Hour)
	replay, err := st.AdvanceWholesale(payer, payee, "session", 2, big.NewInt(50), 1, "", expired)
	if err != nil || !replay.Replayed || replay.Authorization.BilledWei != "20" || replay.BilledDelta.Sign() != 0 {
		t.Fatalf("expired historical replay %+v %v", replay, err)
	}
	if _, err = st.SettleWholesale(payer, payee, "session", 4, 3, expired); err != nil {
		t.Fatal(err)
	}
	before, err := st.GetWholesaleAccount(payer, payee)
	if err != nil {
		t.Fatal(err)
	}
	replay, err = st.AdvanceWholesale(payer, payee, "session", 2, big.NewInt(50), 1, "", expired)
	if err != nil || !replay.Replayed || replay.Authorization.BilledWei != "20" || replay.Authorization.State != AuthorizationAdmitted {
		t.Fatalf("settled historical replay %+v %v", replay, err)
	}
	after, err := st.GetWholesaleAccount(payer, payee)
	if err != nil || before.Version != after.Version || after.DebitedWei != "40" || after.ReservedWei != "0" {
		t.Fatalf("replay moved funds %+v %v", after, err)
	}
	for _, change := range []struct {
		payer, payee []byte
		units        uint64
		reserve      int64
	}{{payer, payee, 3, 50}, {payer, payee, 2, 49}, {payer, bytes20(3), 2, 50}, {bytes20(3), payee, 2, 50}} {
		if _, err = st.AdvanceWholesale(change.payer, change.payee, "session", change.units, big.NewInt(change.reserve), 1, "", expired); err == nil {
			t.Fatal("changed historical advance accepted")
		}
	}
	if _, err = st.AdvanceWholesale(payer, payee, "session", 5, big.NewInt(10), 4, "", expired); err == nil {
		t.Fatal("new advance after settlement accepted")
	}
}
