package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type refusingAdmission struct {
	*payment.Mock
	ambiguous  bool
	failFence  atomic.Bool
	admissions atomic.Int64
	fences     atomic.Int64
}

func (p *refusingAdmission) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	p.admissions.Add(1)
	if p.ambiguous {
		return nil, status.Error(codes.Unavailable, "connection lost")
	}
	return nil, status.Error(codes.FailedPrecondition, "insufficient wholesale account balance: available=0 required=100")
}
func (p *refusingAdmission) CloseUnexecutedAuthorization(_ context.Context, payer []byte, id, reason string, wholesaleAccountID string) error {
	p.fences.Add(1)
	if !bytes.Equal(payer, bytes.Repeat([]byte{1}, 20)) || id != "auth-refused" || reason == "" {
		return errors.New("wrong scope")
	}
	if p.failFence.Load() {
		return errors.New("receiver unavailable")
	}
	return nil
}

func TestRejectedAdmissionProofRequiresScopeExpiryAndReceiverFence(t *testing.T) {
	var fixture struct {
		QuoteVersion uint64 `json:"quote_version"`
		Rejection    int    `json:"rejection_status"`
		Early        int    `json:"before_expiry_status"`
		WrongScope   int    `json:"wrong_scope_status"`
		Unavailable  int    `json:"receiver_unavailable_status"`
		Proof        int    `json:"fenced_proof_status"`
		Outcome      string `json:"proof_outcome"`
	}
	fixtureBytes, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/rejected-admission.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(fixtureBytes, &fixture); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	pc := &refusingAdmission{Mock: payment.NewMock()}
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settlement.key")
	if err = os.WriteFile(path, []byte(hex.EncodeToString(crypto.FromECDSA(key))), 0600); err != nil {
		t.Fatal(err)
	}
	srv, s := newJobOfferBroker(t, &calls, pc, path)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job", strings.NewReader(`{"messages":[]}`))
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	req.Header.Set(livepeerheader.RequestID, "refused")
	setJobTestAuthorization(t, req, srv.URL)
	raw, _ := base64.StdEncoding.DecodeString(req.Header.Get(livepeerheader.Authorization))
	var auth pb.SpendAuthorization
	if err = proto.Unmarshal(raw, &auth); err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Second)
	auth.Payload.ExpiresAt = expiry.UTC().Format(time.RFC3339Nano)
	auth.Payload.AcceptedPrice.QuoteRef = &pb.QuoteRef{QuoteId: "q-1", QuoteVersion: fixture.QuoteVersion, ConstraintFingerprint: []byte{0xaa, 0xbb}, RouteFingerprint: []byte{0xcc, 0xdd}}
	raw, _ = proto.Marshal(&auth)
	req.Header.Set(livepeerheader.Authorization, base64.StdEncoding.EncodeToString(raw))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != fixture.Rejection || calls.Load() != 0 {
		t.Fatalf("status=%d execution=%d", resp.StatusCode, calls.Load())
	}
	record, err := s.sessionStore.JobByRequestID("refused")
	if err != nil {
		t.Fatal(err)
	}
	if record.State != sessionstore.JobPaymentRejected || !bytes.Equal(record.RejectedAuthorization, raw) {
		t.Fatalf("lost rejection evidence: %+v", record)
	}
	query := strings.ReplaceAll(strings.ReplaceAll(naBody(), strings.Repeat("0a", 20), strings.Repeat("01", 20)), strings.Repeat("0b", 20), strings.Repeat("02", 20))
	query = strings.Replace(query, "work-abc", "auth-refused", 1)
	query = strings.Replace(query, `"quote_version":1`, `"quote_version":0`, 1)
	early := askNonAdmission(t, srv, "refused", query)
	early.Body.Close()
	if early.StatusCode != fixture.Early || pc.fences.Load() != 0 {
		t.Fatal("pre-expiry refusal was fenced")
	}
	time.Sleep(time.Until(expiry) + 10*time.Millisecond)
	wrong := askNonAdmission(t, srv, "refused", strings.Replace(query, "auth-refused", "another-auth", 1))
	wrong.Body.Close()
	if wrong.StatusCode != fixture.WrongScope || pc.fences.Load() != 0 {
		t.Fatal("caller changed fenced authorization")
	}
	pc.failFence.Store(true)
	failed := askNonAdmission(t, srv, "refused", query)
	failed.Body.Close()
	if failed.StatusCode != fixture.Unavailable {
		t.Fatalf("failed receiver fence: %d", failed.StatusCode)
	}
	if _, found, _ := s.sessionStore.NonAdmissionFor("refused"); found {
		t.Fatal("proof issued without receiver fence")
	}
	pc.failFence.Store(false)
	proof := askNonAdmission(t, srv, "refused", query)
	var body struct {
		Outcome  string `json:"outcome"`
		Evidence string `json:"non_admission"`
	}
	json.NewDecoder(proof.Body).Decode(&body)
	proof.Body.Close()
	if proof.StatusCode != fixture.Proof || body.Outcome != fixture.Outcome {
		t.Fatalf("proof %d %+v", proof.StatusCode, body)
	}
	decoded := decodeNonAdmission(t, body.Evidence)
	if decoded.WorkId != "auth-refused" {
		t.Fatal("wrong proof scope")
	}
	again := askNonAdmission(t, srv, "refused", query)
	var repeated struct {
		Evidence string `json:"non_admission"`
	}
	json.NewDecoder(again.Body).Decode(&repeated)
	again.Body.Close()
	if repeated.Evidence != body.Evidence || pc.fences.Load() != 2 {
		t.Fatal("proof replay changed evidence or re-fenced")
	}
	exchange, err := http.Get(srv.URL + "/v1/exchange/refused")
	if err != nil {
		t.Fatal(err)
	}
	var ex map[string]any
	json.NewDecoder(exchange.Body).Decode(&ex)
	exchange.Body.Close()
	if ex["outcome"] != fixture.Outcome {
		t.Fatalf("exchange=%v", ex)
	}
	// Exact request replay cannot re-enter payment after proof issuance.
	retry, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/job", strings.NewReader(`{"messages":[]}`))
	retry.Header = req.Header.Clone()
	replay, err := http.DefaultClient.Do(retry)
	if err != nil {
		t.Fatal(err)
	}
	replay.Body.Close()
	if replay.StatusCode != fixture.Rejection || pc.admissions.Load() != 1 || calls.Load() != 0 {
		t.Fatal("fenced request ran again")
	}
}

func TestAmbiguousAdmissionNeverCreatesRejectedProof(t *testing.T) {
	var calls atomic.Int64
	pc := &refusingAdmission{Mock: payment.NewMock(), ambiguous: true}
	srv, s := newJobTestServerWith(t, &calls, pc)
	resp := jobReq(t, srv, "ambiguous", "")
	resp.Body.Close()
	rec, err := s.sessionStore.JobByRequestID("ambiguous")
	if err != nil {
		t.Fatal(err)
	}
	if rec.State == sessionstore.JobPaymentRejected || len(rec.RejectedAuthorization) != 0 || calls.Load() != 0 {
		t.Fatal("ambiguous error classified as non-admission")
	}
}
