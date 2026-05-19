package sessioncontrolexternalmedia

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/server/middleware"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestServeSessionOpen_HappyPath(t *testing.T) {
	store := NewStore()
	d := New(store, DefaultConfig())

	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		OfferingID:      "default",
		InteractionMode: Mode,
		Backend: config.Backend{
			Transport: "http",
			URL:       "http://scope:8000",
		},
		Extra: map[string]any{
			"media_schema":       "scope-passthrough/v0",
			"session_start_path": "/api/v1/session/start",
			"session_stop_path":  "/api/v1/session/stop",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/cap", strings.NewReader(`{}`))
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	if err := d.Serve(context.Background(), modes.Params{
		Writer:     w,
		Request:    req,
		Capability: cap,
	}); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	resp := w.Result()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status: got %d, want 202", resp.StatusCode)
	}

	var body sessionOpenResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.HasPrefix(body.SessionID, "sess_") {
		t.Fatalf("session_id: got %q, want sess_<hex>", body.SessionID)
	}
	if !strings.HasPrefix(body.ControlURL, "ws://broker.example.com/v1/cap/"+body.SessionID+"/control") {
		t.Fatalf("control_url: got %q", body.ControlURL)
	}
	if body.Media.Schema != "scope-passthrough/v0" {
		t.Fatalf("media.schema: got %q", body.Media.Schema)
	}
	if !strings.HasPrefix(body.Media.ScopeURL, "http://broker.example.com/_scope/"+body.SessionID+"/") {
		t.Fatalf("media.scope_url: got %q", body.Media.ScopeURL)
	}
	if body.ExpiresAt == "" {
		t.Fatal("expires_at: empty")
	}

	if got := store.Get(body.SessionID); got == nil {
		t.Fatal("session not registered in store")
	}
}

func TestServeSessionOpen_RequiresBackendURL(t *testing.T) {
	store := NewStore()
	d := New(store, DefaultConfig())

	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		OfferingID:      "default",
		InteractionMode: Mode,
		Backend:         config.Backend{Transport: "http"},
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", strings.NewReader(`{}`))
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	_ = d.Serve(context.Background(), modes.Params{Writer: w, Request: req, Capability: cap})
	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Fatalf("missing backend.url should 500; got %d", w.Result().StatusCode)
	}
}

func TestServeSessionOpen_RejectsNonPOST(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	cap := &config.Capability{
		ID:              "daydream:scope:v1",
		InteractionMode: Mode,
		Backend:         config.Backend{Transport: "http", URL: "http://scope:8000"},
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/cap", nil)
	req.Host = "broker.example.com"
	w := httptest.NewRecorder()

	_ = d.Serve(context.Background(), modes.Params{Writer: w, Request: req, Capability: cap})
	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("non-POST should 405; got %d", w.Result().StatusCode)
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

// TestEmitSessionEndedIncludesSettlement seeds a SessionRecord with
// payment context + live counter, attaches an outbound channel, and
// asserts emitSessionEnded writes a session.ended envelope whose body
// carries a base64-encoded SettlementRecord reflecting the live
// counter's final value.
func TestEmitSessionEndedIncludesSettlement(t *testing.T) {
	d := New(NewStore(), DefaultConfig())

	live := &stubLiveCounter{}
	live.Add(50)

	inputs := middleware.SettlementInputs{
		PaymentBytes:   makeSessionPaymentBytes(t, 10),
		FundedValueWei: big.NewInt(2000),
		WorkUnit:       "seconds",
	}
	rec := &SessionRecord{
		SessionID:        "sess_ext_settle",
		OpenedAt:         time.Now(),
		ExpiresAt:        time.Now().Add(time.Hour),
		LiveCounter:      live,
		SettlementInputs: &inputs,
	}

	out := make(chan outboundEvent, 4)
	rec.SetOutbound(out)

	d.emitSessionEnded(rec, "session.end")

	select {
	case ev := <-out:
		// The marshaled controlws.Envelope { Type, Seq, Body } —
		// we decode the body and confirm settlement is populated.
		var env struct {
			Type string          `json:"type"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(ev.Body, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		if env.Type != "session.ended" {
			t.Fatalf("envelope type = %q, want session.ended", env.Type)
		}
		var body SessionEndedBody
		if err := json.Unmarshal(env.Body, &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Reason != "session.end" {
			t.Fatalf("body.reason = %q", body.Reason)
		}
		if body.Settlement == "" {
			t.Fatalf("body.settlement empty; want base64-encoded SettlementRecord")
		}
		raw, err := base64.StdEncoding.DecodeString(body.Settlement)
		if err != nil {
			t.Fatalf("base64 decode: %v", err)
		}
		var settlement pb.SettlementRecord
		if err := proto.Unmarshal(raw, &settlement); err != nil {
			t.Fatalf("proto unmarshal: %v", err)
		}
		if got := settlement.GetActualUnits(); got != 50 {
			t.Fatalf("actual_units = %d, want 50", got)
		}
		// 50 * 10 = 500; funded 2000 → OVERFUNDED
		if got := settlement.GetOutcome(); got != pb.SettlementRecord_OVERFUNDED {
			t.Fatalf("outcome = %v, want OVERFUNDED", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for session.ended outbound event")
	}
}

func TestEmitSessionEndedWithoutSettlementInputs(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	rec := &SessionRecord{
		SessionID: "sess_ext_no_settle",
		OpenedAt:  time.Now(),
		ExpiresAt: time.Now().Add(time.Hour),
	}
	out := make(chan outboundEvent, 4)
	rec.SetOutbound(out)

	d.emitSessionEnded(rec, "expired")
	select {
	case ev := <-out:
		var env struct {
			Type string          `json:"type"`
			Body json.RawMessage `json:"body"`
		}
		if err := json.Unmarshal(ev.Body, &env); err != nil {
			t.Fatalf("unmarshal envelope: %v", err)
		}
		var body SessionEndedBody
		if err := json.Unmarshal(env.Body, &body); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}
		if body.Reason != "expired" {
			t.Fatalf("body.reason = %q", body.Reason)
		}
		if body.Settlement != "" {
			t.Fatalf("settlement should be empty for stub payment; got %q", body.Settlement)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out")
	}
}

func TestMode_IsCanonical(t *testing.T) {
	d := New(NewStore(), DefaultConfig())
	if d.Mode() != "session-control-external-media@v0" {
		t.Fatalf("Mode(): got %q", d.Mode())
	}
}

func TestDeriveControlURL_HonorsExternalScheme(t *testing.T) {
	tests := []struct {
		name string
		tls  bool
		xfp  string
		want string
	}{
		{
			name: "plain http",
			want: "ws://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "direct tls",
			tls:  true,
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "tls terminated proxy",
			xfp:  "https",
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
		{
			name: "forwarded proto list",
			xfp:  "https, http",
			want: "wss://broker.example.com/v1/cap/sess_x/control",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/cap", nil)
			req.Host = "broker.example.com"
			if tc.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tc.xfp != "" {
				req.Header.Set("X-Forwarded-Proto", tc.xfp)
			}

			got, err := deriveControlURL(req, "sess_x")
			if err != nil {
				t.Fatalf("deriveControlURL: %v", err)
			}
			if got != tc.want {
				t.Fatalf("control_url: got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDeriveScopeURL_HonorsExternalScheme(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/cap", nil)
	req.Host = "broker.example.com"
	req.Header.Set("X-Forwarded-Proto", "https")

	got, err := deriveScopeURL(req, "sess_x")
	if err != nil {
		t.Fatalf("deriveScopeURL: %v", err)
	}
	want := "https://broker.example.com/_scope/sess_x/"
	if got != want {
		t.Fatalf("scope_url: got %q want %q", got, want)
	}
}
