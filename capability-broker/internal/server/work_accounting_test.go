package server

import (
	"io"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/credentialstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/workledger"
)

func TestRegionalJobBindsActualMemberAndPersistsOneBilledReceipt(t *testing.T) {
	ledger, err := workledger.Open(filepath.Join(t.TempDir(), "work.db"), "pool", "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "broker")
	if err != nil {
		t.Fatal(err)
	}
	defer ledger.Close()
	mock := payment.NewMock()
	wrapped := &workledger.Client{Client: mock, AccountClient: mock, Domain: mock, Store: ledger}
	var calls atomic.Int64
	ts, s := newJobOfferBrokerBare(t, wrapped, "")
	s.workAccounting = wrapped
	s.cfg.PoolID = "pool"
	entry := credentialstore.SyncEntry{CredentialID: "credential", HostID: "h1", Kind: credentialstore.KindBearer, TokenSHA256: credentialstore.HashToken("runner-token"), ExpiresAt: time.Now().Add(time.Hour), PoolID: "pool", MemberEthAddress: "0x1111111111111111111111111111111111111111"}
	if _, err = s.credentialStore.SyncReplace("regional", []credentialstore.SyncEntry{entry}); err != nil {
		t.Fatal(err)
	}
	conn := dialAttach(t, ts)
	results := runnerSideFull(t, conn, func(_, _ string, _ map[string][]string, _ []byte) (int, http.Header, []byte) {
		calls.Add(1)
		return 200, http.Header{"Content-Type": {"application/json"}}, []byte(`{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	})
	result := registerVia(t, conn, results, attachDoc("runner-token", "h1", func(m map[string]any) {
		cap := m["capabilities"].([]any)[0].(map[string]any)
		cap["identity"] = map[string]any{"openai.model": "test-model"}
		cap["transports"] = []any{"unary", "stream"}
	}))
	if result["document"] != "accepted" {
		t.Fatalf("attach: %v", result)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(s.offersEngine.EligiblePairs("default")) != 1 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if len(s.offersEngine.EligiblePairs("default")) != 1 {
		t.Fatal("runner never eligible")
	}
	// Ignore certification calls; only the paid dispatch is asserted here.
	baseline := calls.Load()
	for i := 0; i < 2; i++ {
		resp := jobReq(t, ts, "regional-job", "")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("job status=%d body=%s", resp.StatusCode, body)
		}
	}
	if calls.Load() != baseline+1 {
		t.Fatalf("job replay dispatched again: %d", calls.Load()-baseline)
	}
	if err = ledger.Finalize(100, time.Now()); err != nil {
		t.Fatal(err)
	}
	queue, err := ledger.Undelivered(0)
	if err != nil || len(queue) != 1 {
		t.Fatalf("queue %+v %v", queue, err)
	}
	receipt := queue[0]
	if receipt.MemberEthAddress != entry.MemberEthAddress || receipt.HostEnrollmentID != "h1" || receipt.BackendID == "" || receipt.AttributedRevenueWei != "42" || receipt.OfferingID != "default" || receipt.SourceID != ledger.SourceID {
		t.Fatalf("incorrect attribution %+v", receipt)
	}
}
