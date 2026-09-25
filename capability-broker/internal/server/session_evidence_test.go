package server

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func evidenceSigner(t *testing.T) *settlement.Signer {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(crypto.FromECDSA(key))), 0600); err != nil {
		t.Fatal(err)
	}
	signer, err := settlement.LoadSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

type revisionRejectingPayment struct {
	*payment.Mock
	pending bool
}

func (p *revisionRejectingPayment) AdmitAuthorization(ctx context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	var auth pb.SpendAuthorization
	_ = proto.Unmarshal(req.AuthorizationBytes, &auth)
	if auth.GetPayload().GetPredecessorAuthorizationId() == "" {
		return p.Mock.AdmitAuthorization(ctx, req)
	}
	if p.pending {
		return nil, status.Error(codes.Unavailable, "private upstream failure")
	}
	err, _ := status.New(codes.FailedPrecondition, "private upstream failure").WithDetails(&errdetails.ErrorInfo{Domain: "payments.livepeer.org", Reason: "INSUFFICIENT_WHOLESALE_CREDIT"})
	return nil, err.Err()
}

func TestRevisionHTTPWebSocketAndEvidenceOutcomes(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint("pending=", pending), func(t *testing.T) {
			remote := &revisionRejectingPayment{Mock: payment.NewMock(), pending: pending}
			srv, s := newSessionTestServerConfigured(t, (&fakeSessionRunner{}).handler(), nil, Options{PaymentClient: remote})
			s.settlementSigner = evidenceSigner(t)
			opened := decode(t, sessionOpenReq(t, srv, "outcome-open"))
			id, credential := opened["session_id"].(string), opened["credential"].(string)
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+id+"/topup", strings.NewReader(""))
			req.Header.Set("Authorization", "Bearer "+credential)
			req.Header.Set("Livepeer-Request-Id", "outcome-refill")
			req.Header.Set("Livepeer-Capability", "meet:sfu-room")
			req.Header.Set("Livepeer-Offering", "default")
			setSessionTestAuthorization(t, req, "gws-1", "auth-outcome-open")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			wantCode, wantStatus, wantOutcome := "refill_refused", http.StatusConflict, "refused"
			if pending {
				wantCode, wantStatus, wantOutcome = "revision_pending", http.StatusServiceUnavailable, "pending"
			}
			if resp.StatusCode != wantStatus || resp.Header.Get("Livepeer-Error") != wantCode {
				t.Fatalf("HTTP outcome: %d %s", resp.StatusCode, resp.Header.Get("Livepeer-Error"))
			}
			body := decode(t, resp)
			if body["revision"].(map[string]any)["outcome"] != wantOutcome {
				t.Fatalf("HTTP decision: %v", body)
			}
			conn, _ := wsDial(t, srv.URL, id, credential)
			if conn == nil {
				t.Fatal("WS dial")
			}
			defer conn.Close()
			if err := conn.WriteJSON(map[string]any{"type": "session.topup", "body": map[string]any{"request_id": "outcome-refill", "authorization_header": req.Header.Get("Livepeer-Authorization"), "caller_proof": req.Header.Get("Livepeer-Caller-Proof")}}); err != nil {
				t.Fatal(err)
			}
			frame := wsRead(t, conn)["body"].(map[string]any)
			if frame["code"] != wantCode || frame["revision"].(map[string]any)["outcome"] != wantOutcome {
				t.Fatalf("WS outcome: %v", frame)
			}
			lookup, err := http.Get(srv.URL + "/v1/session/gws-1/revisions/outcome-refill")
			if err != nil {
				t.Fatal(err)
			}
			defer lookup.Body.Close()
			if pending {
				if lookup.StatusCode != 202 || lookup.Header.Get("Livepeer-Revision-Evidence") != "" {
					t.Fatal("uncertain admission produced final proof")
				}
				return
			}
			if lookup.StatusCode != 200 {
				t.Fatalf("refused evidence: %d", lookup.StatusCode)
			}
			first := lookup.Header.Get("Livepeer-Revision-Evidence")
			env := verifiedEnvelope(t, first, s.settlementSigner)
			var record pb.SessionRevisionRecord
			if err := protojson.Unmarshal(env.Payload, &record); err != nil || record.GetOutcome() != pb.SessionRevisionRecord_NOT_ADMITTED || record.GetNonAdmissionBasis() != "receiver_fenced" || record.GetReason() != "INSUFFICIENT_WHOLESALE_CREDIT" {
				t.Fatal("refused record invalid", err)
			}
			if err := s.sessionStore.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.initSessionEngine(); err != nil {
				t.Fatal(err)
			}
			replay, err := http.Get(srv.URL + "/v1/session/" + id + "/revisions/outcome-refill")
			if err != nil {
				t.Fatal(err)
			}
			defer replay.Body.Close()
			if replay.Header.Get("Livepeer-Revision-Evidence") != first {
				t.Fatal("refused evidence changed on restart")
			}
		})
	}
}
func verifiedEnvelope(t *testing.T, encoded string, signer *settlement.Signer) settlement.Envelope {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var env settlement.Envelope
	if err := json.Unmarshal(raw, &env); err != nil || env.Signature == nil {
		t.Fatal("missing signature", err)
	}
	sig, err := hex.DecodeString(strings.TrimPrefix(env.Signature.Value, "0x"))
	if err != nil || len(sig) != 65 {
		t.Fatal("invalid signature", err)
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	digest := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(env.Payload))), env.Payload)
	pub, err := crypto.SigToPub(digest, sig)
	if err != nil || "0x"+hex.EncodeToString(crypto.FromECDSAPub(pub)) != signer.PublicKeyHex() {
		t.Fatal("signature mismatch", err)
	}
	return env
}

func TestTerminalLookupCloseRestartReplaySameSignedEvidence(t *testing.T) {
	srv, s := newSessionTestServerConfigured(t, (&fakeSessionRunner{}).handler(), nil)
	s.settlementSigner = evidenceSigner(t)
	opened := decode(t, sessionOpenWithGatewayID(t, srv, "evidence-open", "loc-evidence"))
	id := opened["session_id"].(string)
	credential := opened["credential"].(string)
	if _, err := s.sessionEngine.End(context.Background(), id, "gateway_close"); err != nil {
		t.Fatal(err)
	}
	// Historical zero-sequence record is repaired on its first publication.
	if err := s.sessionStore.Update(id, func(r *sessionstore.Record) error { r.SettlementSeq = 0; r.TerminalSettlement = nil; return nil }); err != nil {
		t.Fatal(err)
	}
	var first string
	for i := 0; i < 4; i++ {
		var response *http.Response
		var err error
		if i%2 == 0 {
			response, err = http.Get(srv.URL + "/v1/settlement/" + id)
		} else {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+id+"/end", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+credential)
			response, err = http.DefaultClient.Do(req)
		}
		if err != nil || response.StatusCode != 200 {
			t.Fatal("publication", err)
		}
		envelope := response.Header.Get("Livepeer-Settlement")
		response.Body.Close()
		if first == "" {
			first = envelope
			env := verifiedEnvelope(t, envelope, s.settlementSigner)
			var record pb.SettlementRecord
			if err := protojson.Unmarshal(env.Payload, &record); err != nil || record.GetSettlementSeq() == 0 || record.GetState() != "closed" {
				t.Fatal("invalid terminal record", err)
			}
		} else if first != envelope {
			t.Fatal("terminal envelope changed")
		}
		if i == 1 {
			if err := s.sessionStore.Close(); err != nil {
				t.Fatal(err)
			}
			if err := s.initSessionEngine(); err != nil {
				t.Fatal(err)
			}
			// A key change cannot re-sign an already-issued terminal fact.
			s.settlementSigner = evidenceSigner(t)
		}
	}
}

func TestRevisionLookupSignedOutcomeAndGenericNonAdmissionGuard(t *testing.T) {
	srv, s := newSessionTestServerConfigured(t, (&fakeSessionRunner{}).handler(), nil)
	s.settlementSigner = evidenceSigner(t)
	opened := decode(t, sessionOpenWithGatewayID(t, srv, "revision-open", "loc-revisions"))
	id := opened["session_id"].(string)
	// Exercise the engine and receiver adapter; envelope scope is still checked
	// by the ordinary HTTP/WS revision tests.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/session/"+id+"/topup", strings.NewReader(""))
	req.Header.Set("Livepeer-Request-Id", "refill-evidence")
	req.Header.Set("Livepeer-Capability", "meet:sfu-room")
	req.Header.Set("Livepeer-Offering", "default")
	setSessionTestAuthorization(t, req, "loc-revisions", "auth-revision-open")
	wire, _ := base64.StdEncoding.DecodeString(req.Header.Get("Livepeer-Authorization"))
	if _, err := s.sessionEngine.ReviseAuthorization(context.Background(), id, "refill-evidence", wire, nil, big.NewInt(100)); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		response, err := http.Get(srv.URL + "/v1/session/" + id + "/revisions/refill-evidence")
		if err != nil || response.StatusCode != 200 {
			t.Fatalf("lookup: %v %v", response, err)
		}
		defer response.Body.Close()
		return response.Header.Get("Livepeer-Revision-Evidence")
	}
	first := read()
	env := verifiedEnvelope(t, first, s.settlementSigner)
	var record pb.SessionRevisionRecord
	if err := protojson.Unmarshal(env.Payload, &record); err != nil || record.GetOutcome() != pb.SessionRevisionRecord_ADMITTED || record.GetSessionId() != id || record.GetAuthorizationId() != "auth-refill-evidence" {
		t.Fatal("revision record", err)
	}
	if first != read() {
		t.Fatal("revision envelope changed")
	}
	if _, err := s.sessionStore.RecordRevisionEnvelope(id, "refill-evidence", []byte("tampered"), first); err == nil {
		t.Fatal("changed evidence accepted")
	}
	response := askNonAdmission(t, srv, "refill-evidence", naBody())
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict || response.Header.Get("Livepeer-Error") != "revision_evidence_required" {
		t.Fatal("accepted refill got non-admission evidence")
	}
}
