package server

import (
	"encoding/base64"
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

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// End-to-end paid-job surface: transport negotiation, extractor claim,
// idempotent replay, request-id reuse rejection — over real HTTP with
// a fake backend and the mock payment daemon.

func newJobTestServer(t *testing.T, backendCalls *atomic.Int64) *httptest.Server {
	t.Helper()
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendCalls.Add(1)
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"chunk\":1}\n\ndata: {\"usage\":{\"total_tokens\":21}}\n\ndata: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"text":"hi"}],"usage":{"total_tokens":42}}`)
	}))
	t.Cleanup(be.Close)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, make([]byte, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Identity:      config.Identity{OrchEthAddress: "0x" + strings.Repeat("cd", 20)},
		PaymentDaemon: config.PaymentDaemon{Mock: true},
		SessionStore: config.SessionStore{
			Path:           filepath.Join(dir, "state.db"),
			SealingKeyFile: keyPath,
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
	return srv
}

func jobReq(t *testing.T, srv *httptest.Server, requestID, accept string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"model":"test-model","messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// jobReqBody posts a job with a chosen body, for the tests that care
// what the body was rather than how it was negotiated.
func jobReqBody(t *testing.T, srv *httptest.Server, requestID, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job", strings.NewReader(body))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub-payment")))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestJobSurfaceEndToEnd(t *testing.T) {
	var backendCalls atomic.Int64
	srv := newJobTestServer(t, &backendCalls)

	// --- unary happy path: extractor claims 42 tokens.
	resp := jobReq(t, srv, "job-req-1", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unary status %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get(livepeerheader.WorkUnits); got != "42" {
		t.Fatalf("Work-Units %q, want 42", got)
	}
	if got := resp.Header.Get(livepeerheader.WorkUnitName); got != "tokens" {
		t.Fatalf("Work-Unit %q, want tokens", got)
	}
	jobID := resp.Header.Get(livepeerheader.JobID)
	if jobID == "" {
		t.Fatal("missing Livepeer-Job-Id")
	}
	if !strings.Contains(string(body), "choices") {
		t.Fatalf("backend body not passed through: %s", body)
	}

	// --- idempotent replay: recorded outcome, no second backend call.
	before := backendCalls.Load()
	replay := jobReq(t, srv, "job-req-1", "")
	rbody, _ := io.ReadAll(replay.Body)
	replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("replay status %d", replay.StatusCode)
	}
	if got := replay.Header.Get(livepeerheader.WorkUnits); got != "42" {
		t.Fatalf("replay Work-Units %q, want 42", got)
	}
	if replay.Header.Get(livepeerheader.JobID) != jobID {
		t.Fatal("replay changed job id")
	}
	if !strings.Contains(string(rbody), `"replayed":true`) {
		t.Fatalf("replay body: %s", rbody)
	}
	if backendCalls.Load() != before {
		t.Fatal("replay re-executed the backend")
	}

	// --- request-id reuse with different content: rejected.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"different":"body-length-changes-fingerprint"}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "job-req-1")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("other-payment")))
	reuse, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	reuse.Body.Close()
	if reuse.StatusCode != http.StatusBadRequest ||
		reuse.Header.Get(livepeerheader.Error) != livepeerheader.ErrRequestIDReuse {
		t.Fatalf("reuse: status %d error %q", reuse.StatusCode, reuse.Header.Get(livepeerheader.Error))
	}

	// --- undeclared transport refused pre-payment (multipart not declared).
	mreq, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job", strings.NewReader("--x--"))
	mreq.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	mreq.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	mreq.Header.Set(livepeerheader.Offering, "default")
	mreq.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	mreq.Header.Set(livepeerheader.RequestID, "job-req-mp")
	mreq.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("stub")))
	mresp, err := http.DefaultClient.Do(mreq)
	if err != nil {
		t.Fatal(err)
	}
	mresp.Body.Close()
	if mresp.StatusCode != http.StatusBadRequest ||
		mresp.Header.Get(livepeerheader.Error) != livepeerheader.ErrTransportUnsupported {
		t.Fatalf("transport refusal: %d %q", mresp.StatusCode, mresp.Header.Get(livepeerheader.Error))
	}

	// --- stream transport: body piped, claim in the trailer.
	sresp := jobReq(t, srv, "job-req-stream", "text/event-stream")
	sbody, _ := io.ReadAll(sresp.Body)
	sresp.Body.Close()
	if sresp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", sresp.StatusCode)
	}
	if !strings.Contains(string(sbody), "[DONE]") {
		t.Fatalf("stream body: %s", sbody)
	}
	if got := sresp.Trailer.Get(livepeerheader.WorkUnits); got != "21" {
		t.Fatalf("stream trailer Work-Units %q, want 21 (trailers: %v)", got, sresp.Trailer)
	}
}

// TestJobReplayRefusesADifferentBody: the defect this closes. The
// envelope fingerprint matches on a retry that reuses the request id and
// the payment, so binding only the envelope — or the body's length —
// let a changed body receive the first exchange's recorded outcome. A
// wrong answer, not an error.
func TestJobReplayRefusesADifferentBody(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	first := jobReqBody(t, srv, "req-body-1", `{"prompt":"alpha"}`)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first exchange status %d", first.StatusCode)
	}
	_ = first.Body.Close()

	// Same id, same envelope, different body of the same length.
	replay := jobReqBody(t, srv, "req-body-1", `{"prompt":"beta_"}`)
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusBadRequest {
		t.Fatalf("changed body replayed with status %d; want 400", replay.StatusCode)
	}
	if got := replay.Header.Get(livepeerheader.Error); got != livepeerheader.ErrRequestIDReuse {
		t.Fatalf("Livepeer-Error = %q; want %q", got, livepeerheader.ErrRequestIDReuse)
	}
}

// TestJobReplayWithTheSameBodyStillReplays: binding the body must not
// break the retry the idempotency contract exists for.
func TestJobReplayWithTheSameBodyStillReplays(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	body := `{"prompt":"alpha"}`
	first := jobReqBody(t, srv, "req-body-2", body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first exchange status %d", first.StatusCode)
	}
	jobID := first.Header.Get(livepeerheader.JobID)
	_ = first.Body.Close()

	replay := jobReqBody(t, srv, "req-body-2", body)
	defer replay.Body.Close()
	if replay.StatusCode != http.StatusOK {
		t.Fatalf("identical retry status %d; want the recorded 200", replay.StatusCode)
	}
	if got := replay.Header.Get(livepeerheader.JobID); got != jobID {
		t.Fatalf("replay job id = %q; want the recorded %q", got, jobID)
	}
}

// TestStreamedJobClaimIsQueryable is the portability fix LOC asked for.
// A streamed job's terminal claim arrives in an HTTP trailer, which Go
// reads and HTTPX, Fetch and reqwest do not. A caller that could not see
// it must be able to ask, or it has to choose between billing zero
// (fails open) and blocking.
func TestStreamedJobClaimIsQueryable(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	resp := jobReq(t, srv, "req-stream-q", "text/event-stream")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	jobID := resp.Header.Get(livepeerheader.JobID)
	if jobID == "" {
		t.Fatal("no Livepeer-Job-Id to query with")
	}
	// Read the body out, exactly as a client that cannot see trailers
	// would, and discard what it could not reach.
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	q, err := http.Get(srv.URL + "/v1/settlement/" + jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Body.Close()
	if q.StatusCode != http.StatusOK {
		t.Fatalf("settlement query status %d", q.StatusCode)
	}
	if got := q.Header.Get(livepeerheader.WorkUnits); got == "" || got == "0" {
		t.Fatalf("queried work units = %q; want the terminal claim", got)
	}
	if got := q.Header.Get(livepeerheader.WorkUnitName); got == "" {
		t.Fatal("query did not report the unit name")
	}
	var body map[string]any
	if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["job_id"] != jobID {
		t.Fatalf("query answered for %v; want %s", body["job_id"], jobID)
	}
	if body["state"] != "terminal" {
		t.Fatalf("state = %v; want terminal", body["state"])
	}
}

// TestSettlementQueryUnknownIDIsNotAClaim: an id the broker does not
// know must never read as a zero claim.
func TestSettlementQueryUnknownIDIsNotAClaim(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	q, err := http.Get(srv.URL + "/v1/settlement/job_does-not-exist")
	if err != nil {
		t.Fatal(err)
	}
	defer q.Body.Close()
	if q.StatusCode == http.StatusOK {
		t.Fatal("unknown id answered 200; a caller would bill it as zero")
	}
}

// The signed job settlement must carry the gateway's own request id.
//
// LOC asked for this: job_id is broker-minted and reaches a
// clearinghouse only through the customer-controlled SDK, and work_id is
// shared by every job on a ticket session. Neither binds the record to
// the durable job the consumer already holds. This is the job path's
// counterpart to gateway_session_id.
func TestJobSettlementCarriesRequestID(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	const requestID = "loc-job-7f3a"
	// A real payment envelope, not the stub the other tests use: the
	// settlement is built from the price the sender signed, so a stub
	// produces no record at all.
	pay := &pb.Payment{ExpectedPrice: &pb.PriceInfo{
		PricePerUnit:  1,
		PixelsPerUnit: 1,
		Constraint: "cap=openai:chat-completions;off=default;wu=tokens;est=42;" +
			"qid=quote-1;qv=1;cfp=aabb;rfp=ccdd",
	}}
	rawPay, err := proto.Marshal(pay)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"model":"test-model","messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString(rawPay))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	// Read it back the way a clearinghouse does: the durable record via
	// the query surface, not the trailer its HTTP client cannot see.
	jobID := resp.Header.Get(livepeerheader.JobID)
	q, err := http.Get(srv.URL + "/v1/settlement/" + jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Body.Close()
	if q.StatusCode != http.StatusOK {
		t.Fatalf("settlement query status %d", q.StatusCode)
	}
	var qb struct {
		Settlement string `json:"settlement"`
	}
	if err := json.NewDecoder(q.Body).Decode(&qb); err != nil {
		t.Fatal(err)
	}
	encoded := qb.Settlement
	rec := decodeSettlementHeader(t, encoded)
	if got := rec.GetRequestId(); got != requestID {
		t.Fatalf("settlement request_id = %q; want the gateway's own %q — without it the "+
			"record cannot be bound to the caller's job", got, requestID)
	}
	// It must be INSIDE the signature, not merely alongside it: a field
	// outside the payload can be rewritten in transit by the very
	// channel the signature exists to distrust.
	if !strings.Contains(rawSettlementPayload(t, encoded), requestID) {
		t.Fatal("request_id is not in the signed payload")
	}
}

// decodeSettlementHeader unwraps the base64 envelope and returns the
// record inside it.
func decodeSettlementHeader(t *testing.T, header string) *pb.SettlementRecord {
	t.Helper()
	if header == "" {
		t.Fatal("no Livepeer-Settlement header")
	}
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatalf("settlement base64: %v", err)
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("settlement envelope: %v", err)
	}
	var rec pb.SettlementRecord
	if err := protojson.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatalf("settlement payload: %v", err)
	}
	return &rec
}

func rawSettlementPayload(t *testing.T, header string) string {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(header)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return string(env.Payload)
}

// jobReqPaid sends a job with a real payment envelope, so the exchange
// actually produces a settlement. The stub envelope other tests use
// parses to no expected_price, and no settlement is built at all.
func jobReqPaid(t *testing.T, srv *httptest.Server, requestID, accept string) *http.Response {
	t.Helper()
	pay := &pb.Payment{ExpectedPrice: &pb.PriceInfo{
		PricePerUnit: 1, PixelsPerUnit: 1,
		Constraint: "cap=openai:chat-completions;off=default;wu=tokens;est=42;" +
			"qid=quote-1;qv=1;cfp=aabb;rfp=ccdd",
	}}
	raw, err := proto.Marshal(pay)
	if err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job",
		strings.NewReader(`{"model":"test-model","messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, requestID)
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString(raw))
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// A streamed job delivers the signed settlement in the trailer it
// advertises. This is the transport where a trailer is the only in-band
// channel — headers are committed long before the units are known.
func TestStreamedJobDeliversSettlementTrailer(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	resp := jobReqPaid(t, srv, "trailer-stream", "text/event-stream")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// Trailers are only readable once the body is drained.
	_, _ = io.Copy(io.Discard, resp.Body)

	if got := resp.Trailer.Get(livepeerheader.Settlement); got == "" {
		t.Fatalf("no %s trailer on a streamed job; trailers present: %v",
			livepeerheader.Settlement, resp.Trailer)
	}
	if got := resp.Trailer.Get(livepeerheader.WorkUnits); got == "" || got == "0" {
		t.Fatalf("streamed work units trailer = %q", got)
	}
}

// A unary job must NOT advertise a settlement trailer.
//
// Trailers ride only on chunked responses, and a unary job copies the
// backend's Content-Length — so net/http drops any trailer without a
// word. Advertising one told a trailer-reading client to wait for
// something that was never coming. The settlement for a unary exchange
// is retrieved from GET /v1/settlement/{id}.
func TestUnaryJobAdvertisesNoUndeliverableTrailer(t *testing.T) {
	var calls atomic.Int64
	srv := newJobTestServer(t, &calls)

	resp := jobReqPaid(t, srv, "trailer-unary", "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	_, _ = io.Copy(io.Discard, resp.Body)

	// With Content-Length set the response is not chunked, so Go leaves
	// any declaration in the ordinary header block rather than promoting
	// it to resp.Trailer — which is exactly how the lie was visible.
	if resp.Header.Get("Content-Length") == "" {
		t.Skip("response was not Content-Length delimited; the advertisement would be honest")
	}
	if declared := resp.Header.Get("Trailer"); strings.Contains(declared, livepeerheader.Settlement) {
		t.Fatalf("unary response advertises %q as a trailer it cannot send (Trailer: %q); "+
			"a client that waits for it waits forever",
			livepeerheader.Settlement, declared)
	}
	if got := resp.Trailer.Get(livepeerheader.Settlement); got != "" {
		t.Fatalf("unexpected settlement trailer on a unary response: %q", got)
	}

	// And the settlement is still reachable — the channel that works.
	jobID := resp.Header.Get(livepeerheader.JobID)
	q, err := http.Get(srv.URL + "/v1/settlement/" + jobID)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Body.Close()
	var body struct {
		Settlement string `json:"settlement"`
	}
	if err := json.NewDecoder(q.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Settlement == "" {
		t.Fatal("unary settlement unreachable: no trailer AND no queryable record")
	}
}
