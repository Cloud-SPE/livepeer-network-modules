package server

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/encoding/protojson"
)

const naBody = `{"protocol":"paid-job/v1","work_id":"work-abc","sender":"0a0b0c",` +
	`"recipient":"0d0e0f","quote_id":"q-1","quote_version":1}`

func askNonAdmission(t *testing.T, srv *httptest.Server, requestID, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(srv.URL+"/v1/non-admission/"+requestID,
		"application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeNonAdmission(t *testing.T, encoded string) *pb.NonAdmissionRecord {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("envelope is not base64: %v", err)
	}
	var env struct {
		Payload   json.RawMessage `json:"payload"`
		Signature *struct {
			Value string `json:"value"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env.Signature == nil || env.Signature.Value == "" {
		t.Fatal("non-admission record is UNSIGNED; an anonymous claim that somebody be refunded")
	}
	var rec pb.NonAdmissionRecord
	if err := protojson.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	return &rec
}

// The claim a consumer needs, keyed on the id IT issued — retrievable
// without anything the customer holds, which is the point: a customer
// that took the work and hid the receipt cannot also suppress this.
func TestNonAdmissionSignedForUnknownRequest(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	resp := askNonAdmission(t, srv, "loc-job-never-sent", naBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	var body struct {
		NonAdmission string `json:"non_admission"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	rec := decodeNonAdmission(t, body.NonAdmission)

	if rec.GetOutcome() != pb.NonAdmissionRecord_NOT_ADMITTED {
		t.Fatalf("outcome = %v; want NOT_ADMITTED — a zero-unit settlement is a different "+
			"claim and must not be confused with this", rec.GetOutcome())
	}
	if rec.GetRequestId() != "loc-job-never-sent" {
		t.Fatalf("request_id = %q", rec.GetRequestId())
	}
	// Bound so it cannot be replayed against another envelope that
	// happens to carry the same id from a different payer.
	if rec.GetWorkId() != "work-abc" || rec.GetProtocol() != "paid-job/v1" {
		t.Fatalf("bindings lost: work_id=%q protocol=%q", rec.GetWorkId(), rec.GetProtocol())
	}
	if len(rec.GetSender()) == 0 || len(rec.GetRecipient()) == 0 {
		t.Fatal("sender/recipient not bound")
	}
	if rec.GetBrokerEthAddress() == "" {
		t.Fatal("broker identity not bound; nobody to hold the claim against")
	}
	if rec.GetObservedAt() == "" || rec.GetCoverageStartedAt() == "" {
		t.Fatalf("observed_at=%q coverage_started_at=%q; both are required",
			rec.GetObservedAt(), rec.GetCoverageStartedAt())
	}
	if rec.GetAcceptedQuoteRef().GetQuoteId() != "q-1" {
		t.Fatal("quote identity not bound")
	}
}

// A request the broker DID admit must never receive a non-admission
// claim — the whole point is that the two cannot both be issued.
func TestNonAdmissionRefusedForAdmittedRequest(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	const requestID = "loc-job-admitted"
	exchange := jobReqPaid(t, srv, requestID, "")
	_ = exchange.Body.Close()

	resp := askNonAdmission(t, srv, requestID, naBody)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d for an admitted request; want 409 — a broker must not be able to "+
			"sign both a settlement and a non-admission for one exchange", resp.StatusCode)
	}
	if got := resp.Header.Get(livepeerheader.Error); got != livepeerheader.ErrAdmitted {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrAdmitted)
	}
}

// Absence across a gap in the broker's own records is not evidence. A
// store reset after the job was issued cannot tell "never admitted"
// apart from "forgot".
func TestNonAdmissionRefusedWhenCoverageStartsAfterIssuance(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	// The store was created by this test, so its coverage starts now;
	// a job issued in 2020 predates it.
	body := `{"protocol":"paid-job/v1","work_id":"w","job_issued_at":"2020-01-01T00:00:00Z"}`
	resp := askNonAdmission(t, srv, "loc-job-before-coverage", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status %d; want 409 — a broker whose records begin after issuance must not "+
			"attest across the gap", resp.StatusCode)
	}
	if got := resp.Header.Get(livepeerheader.Error); got != livepeerheader.ErrCoverageGap {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrCoverageGap)
	}
}

// Coverage must be durable. If it were stamped per-process, a restart
// would silently re-qualify a broker that had lost its records.
func TestCoverageIsStableAcrossReads(t *testing.T) {
	var calls atomic.Int64
	_, s := newSignedJobTestServer(t, &calls)

	first, err := s.sessionStore.CoverageStartedAt()
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.sessionStore.CoverageStartedAt()
	if err != nil {
		t.Fatal(err)
	}
	if !first.Equal(second) {
		t.Fatalf("coverage moved between reads: %s then %s", first, second)
	}
}

// newSignedJobTestServer is newJobTestServerWith plus a delegated
// settlement key. Non-admission evidence is refused outright without
// one, so an unsigned server cannot exercise any of this.
func newSignedJobTestServer(t *testing.T, backendCalls *atomic.Int64) (*httptest.Server, *Server) {
	t.Helper()
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	}))
	t.Cleanup(be.Close)

	dir := t.TempDir()
	sealPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(sealPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	settlePath := filepath.Join(dir, "settlement.key")
	if err := os.WriteFile(settlePath,
		[]byte(hex.EncodeToString(crypto.FromECDSA(key))), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Identity: config.Identity{
			OrchEthAddress:    "0x" + strings.Repeat("cd", 20),
			SettlementKeyFile: settlePath,
		},
		PaymentDaemon: config.PaymentDaemon{Mock: true},
		SessionStore: config.SessionStore{
			Path:           filepath.Join(dir, "state.db"),
			SealingKeyFile: sealPath,
		},
		Capabilities: []config.Capability{{
			ID:         "openai:chat-completions",
			OfferingID: "default",
			Protocol:   "paid-job/v1",
			Job:        &config.JobCapability{Transports: []string{"unary", "stream"}},
			WorkUnit: config.WorkUnit{
				Name:      "tokens",
				Extractor: map[string]any{"type": "openai-usage"},
			},
			Health:  config.Health{InitialStatus: "ready"},
			Price:   config.Price{AmountWei: "1", PerUnits: 1},
			Backend: config.Backend{Transport: "http", URL: be.URL},
			Extra:   map[string]any{"openai": map[string]any{"model": "test-model"}, "provider": "vllm"},
		}},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("config: %v", err)
	}
	s, err := New(cfg, Options{})
	if err != nil {
		t.Fatalf("server: %v", err)
	}
	t.Cleanup(func() {
		if s.sessionStore != nil {
			_ = s.sessionStore.Close()
		}
	})
	srv := httptest.NewServer(s.mux)
	t.Cleanup(srv.Close)
	return srv, s
}
