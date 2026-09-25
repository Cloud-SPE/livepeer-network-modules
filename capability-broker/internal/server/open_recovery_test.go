package server

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/config"
	"github.com/ethereum/go-ethereum/crypto"
	"net/http"
	"net/http/httptest"
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
	"google.golang.org/protobuf/proto"
)

func TestCanceledOpenNonAdmissionScopeAndLookup(t *testing.T) {
	var calls atomic.Int64
	srv, s := newSignedJobTestServer(t, &calls)
	body := strings.Replace(naBody(), "paid-job/v1", "paid-session/v1", 1)
	payload := &pb.SpendAuthorizationPayload{WholesaleAccountId: "test-account", SettlementDomainId: payment.MockSettlementDomainID, RequestId: "open-uncertain", AuthorizationId: "work-abc", Protocol: "paid-session/v1", Payer: bytes.Repeat([]byte{0x0a}, 20), Payee: bytes.Repeat([]byte{0x0b}, 20), AcceptedPrice: &pb.AcceptedPrice{QuoteRef: &pb.QuoteRef{QuoteId: "q-1", QuoteVersion: 1, ConstraintFingerprint: []byte{0xaa, 0xbb}, RouteFingerprint: []byte{0xcc, 0xdd}}}}
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.sessionStore.ReserveOpen("open-uncertain", nil); err != nil {
		t.Fatal(err)
	}
	if err = s.sessionStore.UpdateReservation("open-uncertain", func(r *sessionstore.OpenReservation) error {
		r.AdmissionIntent = &sessionstore.OpenAdmissionIntent{AuthorizationBytes: wire}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ask := askNonAdmission(t, srv, "open-uncertain", body)
	if ask.StatusCode != 503 || ask.Header.Get(livepeerheader.Error) != "admission_pending" {
		t.Fatalf("pending proof=%d", ask.StatusCode)
	}
	ask.Body.Close()
	checkLookup := func(code int, outcome string) {
		t.Helper()
		resp, err := http.Get(srv.URL + "/v1/exchange/open-uncertain")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var got map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&got)
		if resp.StatusCode != code || got["outcome"] != outcome {
			t.Fatalf("lookup=%d %+v", resp.StatusCode, got)
		}
	}
	checkLookup(202, "IN_FLIGHT")
	if err = s.sessionStore.UpdateReservation("open-uncertain", func(r *sessionstore.OpenReservation) error { r.AdmissionCanceled = true; return nil }); err != nil {
		t.Fatal(err)
	}
	checkLookup(200, "ADMISSION_REJECTED")
	bad := askNonAdmission(t, srv, "open-uncertain", strings.Replace(body, "work-abc", "wrong-authority", 1))
	if bad.StatusCode != 409 {
		t.Fatalf("scope accepted=%d", bad.StatusCode)
	}
	bad.Body.Close()
	wrongAccount := askNonAdmission(t, srv, "open-uncertain", strings.Replace(body, "test-account", "another-account", 1))
	if wrongAccount.StatusCode != 409 {
		t.Fatalf("wrong account scope accepted: %d", wrongAccount.StatusCode)
	}
	wrongAccount.Body.Close()
	first := askNonAdmission(t, srv, "open-uncertain", body)
	first.Body.Close()
	if first.StatusCode != 200 {
		t.Fatalf("canceled proof=%d", first.StatusCode)
	}
	envelope := first.Header.Get(livepeerheader.NonAdmission)
	rec := decodeNonAdmission(t, envelope)
	if rec.GetSettlementDomainId() != payment.MockSettlementDomainID {
		t.Fatal("unbound domain")
	}
	second := askNonAdmission(t, srv, "open-uncertain", body)
	second.Body.Close()
	if second.Header.Get(livepeerheader.NonAdmission) != envelope {
		t.Fatal("proof changed on replay")
	}
	checkLookup(200, "NOT_ADMITTED")
	if _, err = s.sessionStore.RecordNonAdmission("open-uncertain", envelope, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredSessionExchangeLookupReturnsStableEvidence(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":"capacity_reached"}`))
	})
	srv := signedOpenRecoveryServer(t, mux)
	opened := sessionOpenReq(t, srv, "open-capacity")
	opened.Body.Close()
	var prior string
	for i := 0; i < 2; i++ {
		resp, err := http.Get(srv.URL + "/v1/exchange/open-capacity")
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != 200 || body["outcome"] != "SETTLED" || body["session_id"] == "" || resp.Header.Get(livepeerheader.Settlement) == "" {
			t.Fatalf("lookup=%d %+v", resp.StatusCode, body)
		}
		got := resp.Header.Get(livepeerheader.Settlement)
		if i > 0 && got != prior {
			t.Fatal("unstable settlement")
		}
		prior = got
	}
}

func TestLostCreateResponseThroughAttachedRunner(t *testing.T) {
	var starts, stops, reconciles atomic.Int64
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sessions", func(w http.ResponseWriter, r *http.Request) { starts.Add(1); w.WriteHeader(503) })
	mux.HandleFunc("POST /reconcile", func(w http.ResponseWriter, r *http.Request) {
		reconciles.Add(1)
		var body struct {
			SessionID string `json:"session_id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.SessionID == "" {
			t.Error("missing broker identity")
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"session_id": body.SessionID, "outcome": "created", "runner_session_id": "rns_lost"})
	})
	mux.HandleFunc("DELETE /sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "rns_lost" {
			t.Error("wrong runner terminated")
		}
		stops.Add(1)
		w.WriteHeader(204)
	})
	srv := signedOpenRecoveryServer(t, mux)
	first := sessionOpenReq(t, srv, "lost-create")
	first.Body.Close()
	if first.StatusCode < 400 {
		t.Fatal("uncertain create returned success")
	}
	resp, err := http.Get(srv.URL + "/v1/exchange/lost-create")
	if err != nil {
		t.Fatal(err)
	}
	body := decode(t, resp)
	if resp.StatusCode != 200 || body["outcome"] != "SETTLED" {
		t.Fatalf("recovery=%d %+v", resp.StatusCode, body)
	}
	replay := sessionOpenReq(t, srv, "lost-create")
	replay.Body.Close()
	if replay.Header.Get(livepeerheader.Error) != "session_terminal" || starts.Load() != 1 || stops.Load() != 1 || reconciles.Load() != 1 {
		t.Fatalf("effects starts=%d stops=%d reconcile=%d error=%s", starts.Load(), stops.Load(), reconciles.Load(), replay.Header.Get(livepeerheader.Error))
	}
}

func signedOpenRecoveryServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "signer.key")
	if err := os.WriteFile(path, []byte(hex.EncodeToString(crypto.FromECDSA(key))), 0600); err != nil {
		t.Fatal(err)
	}
	srv, _ := newSessionTestServerConfigured(t, handler, func(cfg *config.Config) { cfg.Identity.SettlementKeyFile = path })
	return srv
}
