package workledger

import (
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"
)

// Seed completed metering operations before starting the real broker. Its
// normal durable outbox then finalizes/delivers actual receipts over HTTPS.
func TestRegionalE2EProvisionWork(t *testing.T) {
	path := os.Getenv("REGIONAL_E2E_WORK")
	if path == "" {
		t.Skip("explicit offline e2e fixture only")
	}
	var cfg struct {
		Store, PoolID, Source, Broker, Wallet, Terms string
		Count                                        int
		Value                                        int64
		Round                                        int64
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	st, err := Open(cfg.Store, cfg.PoolID, cfg.Source, cfg.Broker)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for i := 0; i < cfg.Count; i++ {
		id := fmt.Sprintf("regional-job-%04d", i)
		if err = st.Bind(id, Attribution{Member: cfg.Wallet, Backend: "fixture-host|fixture-gpu", Enrollment: "fixture-host", Capability: "fixture:work", Offering: cfg.Broker, RequestID: id, TermsVersion: cfg.Terms}); err != nil {
			t.Fatal(err)
		}
		op, err := st.Prepare(Operation{AuthorizationID: id, Kind: "settle", Sequence: 1, Units: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err = st.Complete(op.ID, big.NewInt(cfg.Value)); err != nil {
			t.Fatal(err)
		}
	}
	if err = st.Finalize(cfg.Round, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err = st.Finalize(cfg.Round+1, time.Now()); err != nil {
		t.Fatal(err)
	}
}
