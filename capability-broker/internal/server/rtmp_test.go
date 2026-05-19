package server

import (
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type stubLiveCounter struct {
	v atomic.Uint64
}

func (s *stubLiveCounter) CurrentUnits() uint64 { return s.v.Load() }
func (s *stubLiveCounter) Add(n uint64)         { s.v.Add(n) }

func makeRTMPSettlementPaymentBytes(t *testing.T, pricePerUnit int64) []byte {
	t.Helper()
	constraint := fmt.Sprintf(
		"cap=cap;off=off;wu=seconds;est=%d;qid=quote-1;qv=1;cfp=%x;rfp=%x",
		60,
		[]byte{0xaa, 0xbb},
		[]byte{0xcc, 0xdd},
	)
	pay := &pb.Payment{
		ExpectedPrice: &pb.PriceInfo{
			PricePerUnit:  pricePerUnit,
			PixelsPerUnit: 1,
			Constraint:    constraint,
		},
	}
	raw, err := proto.Marshal(pay)
	if err != nil {
		t.Fatalf("marshal Payment: %v", err)
	}
	return raw
}

func decodeSettlement(t *testing.T, encoded string) *pb.SettlementRecord {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	var rec pb.SettlementRecord
	if err := proto.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("proto unmarshal: %v", err)
	}
	return &rec
}

// TestRTMPCloseSessionEmitsSettlement seeds a session with full
// settlement inputs, drives the customer close endpoint, and asserts
// the 204 response carries X-Livepeer-Settlement reflecting the
// LiveCounter's final value.
func TestRTMPCloseSessionEmitsSettlement(t *testing.T) {
	store := rtmpingresshlsegress.NewStore()
	srv := &Server{rtmpStore: store}

	live := &stubLiveCounter{}
	live.Add(120)

	inputs := middleware.SettlementInputs{
		PaymentBytes:   makeRTMPSettlementPaymentBytes(t, 10),
		FundedValueWei: big.NewInt(2000),
		WorkUnit:       "seconds",
	}

	rec := &rtmpingresshlsegress.SessionRecord{
		SessionID:        "sess_test",
		StreamKey:        "key",
		CapabilityID:     "video:live.rtmp",
		OfferingID:       "default",
		ExpiresAt:        time.Now().Add(time.Hour),
		OpenedAt:         time.Now(),
		Cancel:           func() {},
		LiveCounter:      live,
		Publishing:       true,
		SettlementInputs: &inputs,
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	r := httptest.NewRequest("POST", "/v1/cap/sess_test/end", nil)
	r.SetPathValue("session_id", "sess_test")
	w := httptest.NewRecorder()
	srv.rtmpCloseSession(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	settlementHeader := w.Header().Get(livepeerheader.Settlement)
	if settlementHeader == "" {
		t.Fatalf("expected X-Livepeer-Settlement header to be set")
	}

	rawSettlement := decodeSettlement(t, settlementHeader)
	if rawSettlement.GetActualUnits() != 120 {
		t.Fatalf("actual_units = %d, want 120", rawSettlement.GetActualUnits())
	}
	if rawSettlement.GetWorkUnitName() != "seconds" {
		t.Fatalf("work_unit_name = %q, want seconds", rawSettlement.GetWorkUnitName())
	}
	// 120 units * 10 wei/unit = 1200; funded 2000 → OVERFUNDED
	if rawSettlement.GetOutcome() != pb.SettlementRecord_OVERFUNDED {
		t.Fatalf("outcome = %v, want OVERFUNDED", rawSettlement.GetOutcome())
	}
	if store.Get("sess_test") != nil {
		t.Fatalf("session still present after close")
	}
}

// TestRTMPCloseSessionWithoutSettlementInputsStillCloses verifies the
// close path is still functional when the session has no settlement
// inputs (legacy/stub payment) — the 204 is returned without any
// X-Livepeer-Settlement header.
func TestRTMPCloseSessionWithoutSettlementInputsStillCloses(t *testing.T) {
	store := rtmpingresshlsegress.NewStore()
	srv := &Server{rtmpStore: store}

	rec := &rtmpingresshlsegress.SessionRecord{
		SessionID: "sess_no_settlement",
		StreamKey: "key",
		ExpiresAt: time.Now().Add(time.Hour),
		OpenedAt:  time.Now(),
		Cancel:    func() {},
	}
	if err := store.Add(rec); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	r := httptest.NewRequest("POST", "/v1/cap/sess_no_settlement/end", nil)
	r.SetPathValue("session_id", "sess_no_settlement")
	w := httptest.NewRecorder()
	srv.rtmpCloseSession(w, r)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", w.Code)
	}
	if got := w.Header().Get(livepeerheader.Settlement); got != "" {
		t.Fatalf("unexpected settlement header on legacy payment: %q", got)
	}
}

func TestRTMPCloseSessionUnknownSession404(t *testing.T) {
	store := rtmpingresshlsegress.NewStore()
	srv := &Server{rtmpStore: store}
	r := httptest.NewRequest("POST", "/v1/cap/unknown/end", nil)
	r.SetPathValue("session_id", "unknown")
	w := httptest.NewRecorder()
	srv.rtmpCloseSession(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}
