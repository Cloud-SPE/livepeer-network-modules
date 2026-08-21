package server

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/encoding/protojson"
)

func naBodyFor(issuedAt string) string {
	return `{"protocol":"paid-job/v1","work_id":"work-abc",` +
		`"sender":"` + strings.Repeat("0a", 20) + `",` +
		`"recipient":"` + strings.Repeat("0b", 20) + `",` +
		`"quote_id":"q-1","quote_version":1,` +
		`"constraint_fingerprint":"aabb","route_fingerprint":"ccdd",` +
		`"job_issued_at":"` + issuedAt + `"}`
}

// naBody is a well-formed query whose job was issued now, so coverage
// (stamped when this test's store was created) covers it.
func naBody() string { return naBodyFor(time.Now().UTC().Format(time.RFC3339Nano)) }

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

	resp := askNonAdmission(t, srv, "loc-job-never-sent", naBody())
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
	qr := rec.GetAcceptedQuoteRef()
	if qr.GetQuoteId() != "q-1" || qr.GetQuoteVersion() != 1 {
		t.Fatal("quote identity not bound")
	}
	// The full snapshotted identity, not just id+version: two quotes can
	// share an id and version and differ in the terms they fingerprint.
	if len(qr.GetConstraintFingerprint()) == 0 || len(qr.GetRouteFingerprint()) == 0 {
		t.Fatal("quote fingerprints not bound")
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

	resp := askNonAdmission(t, srv, requestID, naBody())
	defer resp.Body.Close()
	if got := resp.Header.Get(livepeerheader.Error); got != livepeerheader.ErrAdmitted {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrAdmitted)
	}
	// And it hands back the OUTCOME, not just a refusal. A consumer
	// asking this is about to decide how much to charge; answering only
	// "no" sends it away to charge conservatively while the broker holds
	// the settlement that says otherwise.
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["outcome"].(string); got != "SETTLED" {
		t.Fatalf("outcome = %q; want SETTLED with the recoverable evidence", got)
	}
	if got, _ := body["settlement"].(string); got == "" {
		t.Fatal("no settlement returned; the consumer still cannot avoid overcharging")
	}
	if got, _ := body["job_id"].(string); got == "" {
		t.Fatal("no job_id returned")
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
	resp := askNonAdmission(t, srv, "loc-job-before-coverage", naBodyFor("2020-01-01T00:00:00Z"))
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

// Every field is required and strictly parsed. The old handler failed
// OPEN in the direction that mattered: a missing job_issued_at parsed as
// the zero time and skipped the coverage check, so the one query a
// broker with a records gap must refuse was the one that omitted a
// field.
func TestNonAdmissionRequiresCompleteContext(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	now := time.Now().UTC().Format(time.RFC3339Nano)
	full := `"protocol":"paid-job/v1","work_id":"w","sender":"` + strings.Repeat("0a", 20) +
		`","recipient":"` + strings.Repeat("0b", 20) + `","quote_id":"q","quote_version":1,` +
		`"constraint_fingerprint":"aabb","route_fingerprint":"ccdd","job_issued_at":"` + now + `"`

	cases := map[string]string{
		"missing job_issued_at":   `{"protocol":"paid-job/v1","work_id":"w","sender":"` + strings.Repeat("0a", 20) + `","recipient":"` + strings.Repeat("0b", 20) + `","quote_id":"q","quote_version":1,"constraint_fingerprint":"aabb","route_fingerprint":"ccdd"}`,
		"malformed job_issued_at": `{` + strings.Replace(full, `"job_issued_at":"`+now+`"`, `"job_issued_at":"last tuesday"`, 1) + `}`,
		"empty work_id":           `{` + strings.Replace(full, `"work_id":"w"`, `"work_id":""`, 1) + `}`,
		"unknown protocol":        `{` + strings.Replace(full, `"protocol":"paid-job/v1"`, `"protocol":"whatever/v9"`, 1) + `}`,
		"short sender":            `{` + strings.Replace(full, `"sender":"`+strings.Repeat("0a", 20)+`"`, `"sender":"0a0b"`, 1) + `}`,
		"non-hex recipient":       `{` + strings.Replace(full, `"recipient":"`+strings.Repeat("0b", 20)+`"`, `"recipient":"zzzz"`, 1) + `}`,
		"missing quote_id":        `{` + strings.Replace(full, `"quote_id":"q"`, `"quote_id":""`, 1) + `}`,
		"zero quote_version":      `{` + strings.Replace(full, `"quote_version":1`, `"quote_version":0`, 1) + `}`,
		"missing fingerprints":    `{"protocol":"paid-job/v1","work_id":"w","sender":"` + strings.Repeat("0a", 20) + `","recipient":"` + strings.Repeat("0b", 20) + `","quote_id":"q","quote_version":1,"job_issued_at":"` + now + `"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			resp := askNonAdmission(t, srv, "req-"+strings.ReplaceAll(name, " ", "-"), body)
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status %d; want 400 — signing an incompletely-scoped record produces "+
					"evidence that binds to nothing", resp.StatusCode)
			}
		})
	}
}

// The record must be RETAINED, not just returned. A conflicting pair is
// the accountability the signature exists for, and a record that only
// ever lived in one HTTP response cannot conflict with anything.
func TestNonAdmissionIsRetainedAndStable(t *testing.T) {
	var calls atomic.Int64
	srv, s := newSignedJobTestServer(t, &calls)

	const requestID = "loc-job-retained"
	first := askNonAdmission(t, srv, requestID, naBody())
	var b1 struct {
		NonAdmission string `json:"non_admission"`
	}
	if err := json.NewDecoder(first.Body).Decode(&b1); err != nil {
		t.Fatal(err)
	}
	_ = first.Body.Close()

	stored, found, err := s.sessionStore.NonAdmissionFor(requestID)
	if err != nil || !found {
		t.Fatalf("record not retained (found=%v err=%v)", found, err)
	}
	if stored != b1.NonAdmission {
		t.Fatal("retained record differs from the one returned")
	}

	// Asking again returns the SAME record. Re-signing would produce two
	// signed statements about one fact under different observed_at, and
	// a consumer holding both cannot tell agreement from conflict.
	second := askNonAdmission(t, srv, requestID, naBody())
	var b2 struct {
		NonAdmission string `json:"non_admission"`
	}
	if err := json.NewDecoder(second.Body).Decode(&b2); err != nil {
		t.Fatal(err)
	}
	_ = second.Body.Close()
	if b2.NonAdmission != b1.NonAdmission {
		t.Fatal("a second query re-signed the same fact; one fact, one record")
	}
}

// Having sworn non-admission, the broker must refuse to admit. Otherwise
// it can end up holding a settlement and a non-admission for one
// request, both signed by the same delegated key.
func TestAdmissionRefusedAfterNonAdmission(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	const requestID = "loc-job-sworn"
	na := askNonAdmission(t, srv, requestID, naBody())
	if na.StatusCode != http.StatusOK {
		t.Fatalf("non-admission status %d", na.StatusCode)
	}
	_ = na.Body.Close()

	resp := jobReqPaid(t, srv, requestID, "")
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Fatal("the broker admitted a request it had already sworn it never admitted; " +
			"two contradictory signed claims under one key")
	}
}

// A clearinghouse holds only its own request id. Every other lookup on
// this broker is keyed on something the customer holds, so a customer
// that withheld the settlement could force a conservative full charge
// the broker had evidence against.
func TestExchangeLookupByRequestID(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	const requestID = "loc-job-reconcile"
	exchange := jobReqPaid(t, srv, requestID, "")
	_, _ = io.Copy(io.Discard, exchange.Body)
	_ = exchange.Body.Close()

	resp, err := http.Get(srv.URL + "/v1/exchange/" + requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d; want 200 for a settled exchange", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["outcome"].(string); got != "SETTLED" {
		t.Fatalf("outcome = %q; want SETTLED", got)
	}
	if got, _ := body["settlement"].(string); got == "" {
		t.Fatal("settled exchange returned no settlement envelope")
	}
	if got, _ := body["job_id"].(string); got == "" {
		t.Fatal("no broker job id; the consumer cannot correlate or poll")
	}
}

// An unknown request is NO_RECORD, which is distinct from NOT_ADMITTED:
// this broker has not been asked to attest, and silence is not a claim.
func TestExchangeLookupUnknownIsNotAClaim(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	resp, err := http.Get(srv.URL + "/v1/exchange/never-heard-of-it")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d; want 404", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["outcome"].(string); got != "NO_RECORD" {
		t.Fatalf("outcome = %q; want NO_RECORD — an unasked broker has made no claim", got)
	}
}

// Once a non-admission has been issued, the same lookup surfaces it, so
// a consumer does not have to know which endpoint to try.
func TestExchangeLookupSurfacesNonAdmission(t *testing.T) {
	var calls atomic.Int64
	srv, _ := newSignedJobTestServer(t, &calls)

	const requestID = "loc-job-sworn-lookup"
	na := askNonAdmission(t, srv, requestID, naBody())
	if na.StatusCode != http.StatusOK {
		t.Fatalf("non-admission status %d", na.StatusCode)
	}
	_ = na.Body.Close()

	resp, err := http.Get(srv.URL + "/v1/exchange/" + requestID)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got, _ := body["outcome"].(string); got != "NOT_ADMITTED" {
		t.Fatalf("outcome = %q; want NOT_ADMITTED", got)
	}
	if got, _ := body["non_admission"].(string); got == "" {
		t.Fatal("no signed record returned")
	}
}
