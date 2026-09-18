package store

import (
	"math/big"
	"path/filepath"
	"testing"
	"time"
)

func TestSourceFreezeRequiresSettledAuthorizationsAndSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "receiver.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	payer, payee := bytes20(1), bytes20(2)
	now := time.Now()
	fundLegacySession(t, st, payer, "fund", 100)
	seed := wholesaleSeed("work", payer, payee, "100", now.Add(time.Hour))
	if _, err = st.AdmitWholesale(seed, "fund", big.NewInt(50), now); err != nil {
		t.Fatal(err)
	}
	if count, err := st.ActiveAuthorizations(); err != nil || count != 1 {
		t.Fatalf("active=%d %v", count, err)
	}
	if _, err = st.FreezeSource("retire"); err == nil {
		t.Fatal("active source frozen")
	}
	if _, err = st.SettleWholesale(payer, payee, "work", 0, 1, now); err != nil {
		t.Fatal(err)
	}
	first, err := st.FreezeSource("settled retirement")
	if err != nil || !first.Frozen {
		t.Fatalf("freeze %+v %v", first, err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	again, err := st.FreezeSource("different reason")
	if err != nil || again != first {
		t.Fatalf("fence changed %+v %v", again, err)
	}
	if fence, err := st.SourceFence(); err != nil || fence != first {
		t.Fatalf("restart fence %+v %v", fence, err)
	}
}
