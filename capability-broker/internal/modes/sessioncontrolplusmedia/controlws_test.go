package sessioncontrolplusmedia

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/proto"
)

func makeTimedCtx(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

func TestEnvelopeReservedTypes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		typ  string
		want bool
	}{
		{"started", TypeSessionStarted, true},
		{"end", TypeSessionEnd, true},
		{"ended", TypeSessionEnded, true},
		{"error", TypeSessionError, true},
		{"usage_tick", TypeSessionUsageTick, true},
		{"balance_low", TypeSessionBalanceLow, true},
		{"reconnected", TypeSessionReconnected, true},
		{"media_offer", TypeMediaSDPOffer, true},
		{"media_ice", TypeMediaICECandidate, true},
		{"workload", "set_persona", false},
		{"workload_dotted_unknown", "audio.frame", false},
		{"empty", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsReserved(tc.typ); got != tc.want {
				t.Fatalf("IsReserved(%q) = %v, want %v", tc.typ, got, tc.want)
			}
		})
	}
}

func TestDecodeEnvelope(t *testing.T) {
	t.Parallel()
	good := []byte(`{"type":"x","seq":7,"body":{"k":"v"}}`)
	env, err := decodeEnvelope(good)
	if err != nil {
		t.Fatalf("decodeEnvelope: %v", err)
	}
	if env.Type != "x" || env.Seq != 7 {
		t.Fatalf("envelope mismatch: %+v", env)
	}
	if _, err := decodeEnvelope([]byte(`not json`)); err == nil {
		t.Fatal("expected error on malformed JSON")
	}
	if _, err := decodeEnvelope([]byte(`{"seq":1}`)); err == nil {
		t.Fatal("expected error on empty type")
	}
}

func TestParseLastSeq(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/v1/cap/sess_x/control", nil)
	r.Header.Set("Last-Seq", "12")
	if got := parseLastSeq(r); got != 12 {
		t.Fatalf("Last-Seq header: got %d, want 12", got)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/v1/cap/sess_x/control?last_seq=42", nil)
	if got := parseLastSeq(r2); got != 42 {
		t.Fatalf("last_seq query: got %d, want 42", got)
	}
	r3 := httptest.NewRequest(http.MethodGet, "/v1/cap/sess_x/control", nil)
	if got := parseLastSeq(r3); got != 0 {
		t.Fatalf("absent: got %d, want 0", got)
	}
}

func TestReplayBufferAppendsAndShrinks(t *testing.T) {
	t.Parallel()
	rb := newReplayBuffer(3, 0)
	for i := uint64(1); i <= 5; i++ {
		rb.Append(i, []byte("x"))
	}
	if got := rb.Len(); got != 3 {
		t.Fatalf("Len after overflow: got %d, want 3", got)
	}
	got := rb.Since(2)
	if len(got) != 3 || got[0].seq != 3 {
		t.Fatalf("Since(2): %+v", got)
	}
}

func TestReplayBufferByteCap(t *testing.T) {
	t.Parallel()
	rb := newReplayBuffer(100, 10)
	for i := uint64(1); i <= 5; i++ {
		rb.Append(i, []byte("12345"))
	}
	if rb.Bytes() > 10 {
		t.Fatalf("byte cap not enforced: %d > 10", rb.Bytes())
	}
}

func TestStoreAddRemove(t *testing.T) {
	t.Parallel()
	s := NewStore(StoreConfig{ReplayBufferMessages: 4})
	rec := &SessionRecord{SessionID: "sess_a"}
	if err := s.Add(rec); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(rec); err == nil {
		t.Fatal("expected error on duplicate id")
	}
	if got := s.Get("sess_a"); got != rec {
		t.Fatalf("Get: got %v, want %v", got, rec)
	}
	s.Remove("sess_a")
	if got := s.Get("sess_a"); got != nil {
		t.Fatalf("Get after Remove: got %v, want nil", got)
	}
}

func TestServeControlWS_BasicLifecycle(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 8})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 100 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)

	rec := &SessionRecord{SessionID: "sess_basic", OpenedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	if err := store.Add(rec); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_basic/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read first: %v", err)
	}
	if mt != websocket.TextMessage {
		t.Fatalf("want text frame, got %d", mt)
	}
	var env ControlEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Type != TypeSessionStarted {
		t.Fatalf("first envelope: got type=%q want %q", env.Type, TypeSessionStarted)
	}
	if env.Seq == 0 {
		t.Fatal("expected non-zero seq on session.started")
	}

	end, _ := json.Marshal(ControlEnvelope{Type: TypeSessionEnd})
	if err := conn.WriteMessage(websocket.TextMessage, end); err != nil {
		t.Fatalf("write end: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if store.Get("sess_basic") == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := store.Get("sess_basic"); got != nil {
		t.Fatalf("session.end did not tear down record: still present %v", got)
	}
}

type stubLiveCounter struct {
	v atomic.Uint64
}

func (s *stubLiveCounter) CurrentUnits() uint64 { return s.v.Load() }
func (s *stubLiveCounter) Add(n uint64)         { s.v.Add(n) }

func makeSessionPaymentBytes(t *testing.T, pricePerUnit int64) []byte {
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

// TestServeControlWS_EmitsSessionEndedWithSettlement seeds a session
// with settlement inputs and a live counter, runs the control-WS
// upgrade, sends session.end from the client, and asserts the broker
// emits a session.ended envelope containing a base64-encoded
// SettlementRecord before the close frame.
func TestServeControlWS_EmitsSessionEndedWithSettlement(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 8})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 100 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)

	live := &stubLiveCounter{}
	live.Add(75)

	inputs := middleware.SettlementInputs{
		PaymentBytes:   makeSessionPaymentBytes(t, 10),
		FundedValueWei: big.NewInt(2000),
		WorkUnit:       "seconds",
	}
	rec := &SessionRecord{
		SessionID:        "sess_settle",
		OpenedAt:         time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
		LiveCounter:      live,
		SettlementInputs: &inputs,
	}
	if err := store.Add(rec); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_settle/control"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Drain session.started.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("read first: %v", err)
	}

	// Trigger session.end.
	end, _ := json.Marshal(ControlEnvelope{Type: TypeSessionEnd})
	if err := conn.WriteMessage(websocket.TextMessage, end); err != nil {
		t.Fatalf("write end: %v", err)
	}

	// The next text frame should be the broker's session.ended
	// envelope carrying settlement.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var endedEnv ControlEnvelope
	for {
		mt, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read after end: %v", err)
		}
		if mt != websocket.TextMessage {
			continue
		}
		if err := json.Unmarshal(data, &endedEnv); err != nil {
			t.Fatalf("decode ended envelope: %v", err)
		}
		if endedEnv.Type == TypeSessionEnded {
			break
		}
	}
	var body SessionEndedBody
	if err := json.Unmarshal(endedEnv.Body, &body); err != nil {
		t.Fatalf("decode session.ended body: %v", err)
	}
	if body.Reason != "session.end" {
		t.Fatalf("body.reason = %q, want session.end", body.Reason)
	}
	if body.Settlement == "" {
		t.Fatalf("body.settlement empty; expected base64-encoded SettlementRecord")
	}
	raw, err := base64.StdEncoding.DecodeString(body.Settlement)
	if err != nil {
		t.Fatalf("base64 decode settlement: %v", err)
	}
	var settlement pb.SettlementRecord
	if err := proto.Unmarshal(raw, &settlement); err != nil {
		t.Fatalf("proto unmarshal settlement: %v", err)
	}
	if got := settlement.GetActualUnits(); got != 75 {
		t.Fatalf("actual_units = %d, want 75", got)
	}
	// 75 units * 10 wei/unit = 750; funded 2000 → OVERFUNDED
	if got := settlement.GetOutcome(); got != pb.SettlementRecord_OVERFUNDED {
		t.Fatalf("outcome = %v, want OVERFUNDED", got)
	}
}

func TestServeControlWS_UnknownSession(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 4})
	d := New(store, DefaultControlWSConfig())
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_unknown/control"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial failure for unknown session")
	}
	if resp == nil {
		t.Fatalf("no response: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestServeControlWS_DoubleAttachConflict(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 4})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 200 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)

	rec := &SessionRecord{SessionID: "sess_dup", OpenedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = store.Add(rec)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_dup/control"
	conn1, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("first dial: %v", err)
	}
	defer conn1.Close()
	_ = conn1.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := conn1.ReadMessage(); err != nil {
		t.Fatalf("first read: %v", err)
	}

	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatal("expected dial conflict on simultaneous attach")
	}
	if resp == nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got resp=%v err=%v", resp, err)
	}
}

func TestServeControlWS_ReconnectReplaysSeq(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 8})
	cfg := DefaultControlWSConfig()
	cfg.HeartbeatInterval = 100 * time.Millisecond
	cfg.MissedHeartbeatThreshold = 30
	d := New(store, cfg)

	rec := &SessionRecord{SessionID: "sess_rc", OpenedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = store.Add(rec)
	rec.replay.Append(1, []byte(`{"type":"session.usage.tick","seq":1}`))
	rec.replay.Append(2, []byte(`{"type":"session.usage.tick","seq":2}`))
	rec.nextSeq = 2

	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/cap/{session_id}/control", d.ServeControlWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http", "ws", 1) + "/v1/cap/sess_rc/control"
	hdr := http.Header{}
	hdr.Set("Last-Seq", "1")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	got := []ControlEnvelope{}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && len(got) < 2 {
		_ = conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		_, data, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var env ControlEnvelope
		if err := json.Unmarshal(data, &env); err == nil {
			got = append(got, env)
		}
	}
	if len(got) < 2 {
		t.Fatalf("want >= 2 envelopes, got %d", len(got))
	}
	if got[0].Type != TypeSessionReconnected {
		t.Fatalf("first envelope: got type=%q want %q", got[0].Type, TypeSessionReconnected)
	}
	sawReplay := false
	for _, env := range got[1:] {
		if env.Seq == 2 {
			sawReplay = true
		}
	}
	if !sawReplay {
		t.Fatalf("did not see seq=2 replayed; got=%+v", got)
	}
}

func TestSessionRecordNextSeqMonotonic(t *testing.T) {
	t.Parallel()
	rec := &SessionRecord{SessionID: "x"}
	for i := uint64(1); i <= 100; i++ {
		if got := rec.NextSeq(); got != i {
			t.Fatalf("NextSeq() iter %d: got %d, want %d", i, got, i)
		}
	}
}

func TestReconnectWatchdogTearsDownExpired(t *testing.T) {
	t.Parallel()
	store := NewStore(StoreConfig{ReplayBufferMessages: 4})
	cfg := DefaultControlWSConfig()
	cfg.ReconnectWindow = 50 * time.Millisecond
	d := New(store, cfg)
	rec := &SessionRecord{SessionID: "sess_w", OpenedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour)}
	_ = store.Add(rec)
	rec.SetActive(false)
	rec.SetActive(false)

	ctx, cancel := makeTimedCtx(5 * time.Second)
	defer cancel()
	go store.reconnectWatchdog(ctx, d, 50*time.Millisecond)

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if store.Get("sess_w") == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("watchdog did not tear down expired session")
}

func TestDeriveControlURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		base string
		want string
	}{
		{"http://broker:8080", "ws://broker:8080/v1/cap/sess_x/control"},
		{"https://broker.example.com", "wss://broker.example.com/v1/cap/sess_x/control"},
	} {
		got, err := deriveControlURL(tc.base, "sess_x")
		if err != nil {
			t.Fatalf("deriveControlURL(%q): %v", tc.base, err)
		}
		if got != tc.want {
			t.Fatalf("deriveControlURL(%q): got %q want %q", tc.base, got, tc.want)
		}
	}

	if _, err := deriveControlURL("not-a-url", "x"); err == nil {
		t.Fatal("expected error on missing host")
	}

	u, _ := url.Parse("http://broker:8080")
	_ = u
}
