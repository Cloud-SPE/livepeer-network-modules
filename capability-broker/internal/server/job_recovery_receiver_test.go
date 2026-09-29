package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/sessionstore"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const jobReceiverKey = "0101010101010101010101010101010101010101010101010101010101010101"

type jobLossReceiver struct {
	*payment.GRPC
	store                                                        *sessionstore.Store
	t                                                            *testing.T
	loseAdmission, refuseAdmission, recoveryDown, loseSettlement bool
}

func (p *jobLossReceiver) AdmitAuthorization(ctx context.Context, r payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	var a pb.SpendAuthorization
	_ = proto.Unmarshal(r.AuthorizationBytes, &a)
	rec, err := p.store.JobByRequestID(a.Payload.RequestId)
	if err != nil || rec.Recovery == nil || !bytes.Equal(rec.Recovery.Admission.AuthorizationBytes, r.AuthorizationBytes) {
		p.t.Error("admission preceded durable intent")
	}
	if p.refuseAdmission {
		return nil, errors.New("lost unaccepted call")
	}
	out, err := p.GRPC.AdmitAuthorization(ctx, r)
	if err == nil && p.loseAdmission {
		return nil, errors.New("lost accepted response")
	}
	return out, err
}
func (p *jobLossReceiver) CancelAuthorizationAdmission(ctx context.Context, wire []byte) (*payment.CanceledAdmission, error) {
	if p.recoveryDown {
		return nil, errors.New("recovery unavailable")
	}
	return p.GRPC.CancelAuthorizationAdmission(ctx, wire)
}
func (p *jobLossReceiver) SettleAuthorization(ctx context.Context, r payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	rec, err := p.store.JobByRequestID("real-job")
	if err != nil || rec.Pending == nil || rec.Pending.ActualUnits != r.ActualUnits {
		p.t.Error("settlement preceded durable usage")
	}
	out, err := p.GRPC.SettleAuthorization(ctx, r)
	if err == nil && p.loseSettlement {
		p.loseSettlement = false
		return nil, errors.New("lost settlement response")
	}
	return out, err
}

type failJobUpdate struct {
	jobIdemStore
	calls, fail int
}

func (f *failJobUpdate) UpdateJob(id string, fn func(*sessionstore.JobRecord) error) error {
	f.calls++
	if f.calls == f.fail {
		return errors.New("injected durable write failure")
	}
	return f.jobIdemStore.UpdateJob(id, fn)
}
func TestPaidJobRealReceiverRecovery(t *testing.T) {
	var fixture struct {
		Scenarios []string `json:"scenarios"`
	}
	raw, err := os.ReadFile("../../../livepeer-network-protocol/conformance/fixtures/paid-job-recovery.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range fixture.Scenarios {
		t.Run(scenario, func(t *testing.T) {
			client, restart := receiverForJob(t)
			remote := &jobLossReceiver{GRPC: client, t: t, loseAdmission: scenario == "lost_admission" || scenario == "lost_funded_admission", refuseAdmission: scenario == "unaccepted", recoveryDown: true, loseSettlement: scenario == "lost_settlement"}
			var calls atomic.Int64
			keyPath := filepath.Join(t.TempDir(), "signer")
			if err := os.WriteFile(keyPath, []byte(jobReceiverKey), 0600); err != nil {
				t.Fatal(err)
			}
			srv, s := newJobOfferBroker(t, &calls, remote, keyPath)
			remote.store = s.sessionStore
			for stage, n := range map[string]int{"prepare_write": 1, "admitted_write": 2, "execution_write": 3, "usage_write": 4, "terminal_write": 5} {
				if scenario == stage {
					s.jobIdem = &failJobUpdate{jobIdemStore: s.jobIdem, fail: n}
				}
			}
			request := func() *http.Request {
				req, _ := http.NewRequest("POST", srv.URL+"/v1/job", strings.NewReader(`{"messages":[]}`))
				req.Header.Set(livepeerheader.RequestID, "real-job")
				req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
				req.Header.Set(livepeerheader.Offering, "default")
				req.Header.Set(livepeerheader.Protocol, "paid-job/v1")
				setJobTestAuthorization(t, req, srv.URL)
				raw, _ := base64.StdEncoding.DecodeString(req.Header.Get(livepeerheader.Authorization))
				var auth pb.SpendAuthorization
				_ = proto.Unmarshal(raw, &auth)
				key, _ := crypto.HexToECDSA(jobReceiverKey)
				auth.Payload.Payer = crypto.PubkeyToAddress(key.PublicKey).Bytes()
				auth.Payload.Payee = bytes.Repeat([]byte{0xaa}, 20)
				auth.Payload.NotBefore = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
				auth.Payload.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
				plain, _ := proto.MarshalOptions{Deterministic: true}.Marshal(auth.Payload)
				auth.Signature, _ = crypto.Sign(crypto.Keccak256([]byte("\x19Ethereum Signed Message:\n32"), crypto.Keccak256(plain)), key)
				auth.Signature[64] += 27
				raw, _ = proto.Marshal(&auth)
				req.Header.Set(livepeerheader.Authorization, base64.StdEncoding.EncodeToString(raw))
				return req
			}
			req := request()
			if scenario == "lost_funded_admission" {
				key, _ := crypto.HexToECDSA(jobReceiverKey)
				pay := &pb.Payment{Sender: crypto.PubkeyToAddress(key.PublicKey).Bytes(), TicketParams: &pb.TicketParams{Recipient: bytes.Repeat([]byte{0xaa}, 20), RecipientRandHash: []byte("test-account")}, TicketSenderParams: []*pb.TicketSenderParams{{SenderNonce: 1}}}
				wire, _ := proto.MarshalOptions{Deterministic: true}.Marshal(pay)
				req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString(wire))
				receipt, err := client.GetWholesaleFundingReceipt(t.Context(), wire, "test-account")
				if err != nil || receipt.Credited.Cmp(big.NewInt(1_000_000_000_000_000)) != 0 {
					t.Fatalf("read receipt: %+v %v", receipt, err)
				}
				if _, err = client.GetWholesaleFundingReceipt(t.Context(), wire, "other-account"); status.Code(err) != codes.PermissionDenied {
					t.Fatalf("receipt escaped account: %v", err)
				}
				pay.TicketSenderParams[0].SenderNonce = 2
				absent, _ := proto.MarshalOptions{Deterministic: true}.Marshal(pay)
				if _, err = client.GetWholesaleFundingReceipt(t.Context(), absent, "test-account"); status.Code(err) != codes.NotFound {
					t.Fatalf("missing receipt not read-only: %v", err)
				}
			}
			// A second account holds the same receiver authorization ID under the same
			// payer. No recovery transition may inspect, cancel, settle or release it.
			wire, _ := base64.StdEncoding.DecodeString(req.Header.Get(livepeerheader.Authorization))
			var other pb.SpendAuthorization
			_ = proto.Unmarshal(wire, &other)
			other.Payload.WholesaleAccountId = "other-account"
			key, _ := crypto.HexToECDSA(jobReceiverKey)
			plain, _ := proto.MarshalOptions{Deterministic: true}.Marshal(other.Payload)
			other.Signature, _ = crypto.Sign(crypto.Keccak256([]byte("\x19Ethereum Signed Message:\n32"), crypto.Keccak256(plain)), key)
			other.Signature[64] += 27
			otherWire, _ := proto.Marshal(&other)
			if _, err := client.AdmitAuthorization(t.Context(), payment.AdmitAuthorizationRequest{AuthorizationBytes: otherWire, Reservation: big.NewInt(100)}); err != nil {
				t.Fatal(err)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			// Restart the broker ledger and actual receiver process. Keep HTTP routing
			// quiescent until recovery completes; no runner callback is re-executed.
			path := s.cfg.SessionStore.Path
			seal, err := sessionstore.LoadKeyFile(s.cfg.SessionStore.SealingKeyFile)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.sessionStore.Close(); err != nil {
				t.Fatal(err)
			}
			st, err := sessionstore.Open(path, seal)
			if err != nil {
				t.Fatal(err)
			}
			s.sessionStore = st
			s.jobIdem = &boltJobIdem{store: st}
			remote.store = st
			restart()
			remote.recoveryDown = false
			s.sweepPendingDebits(t.Context())
			rec, err := st.JobByRequestID("real-job")
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "prepare_write":
				if rec.Recovery != nil || calls.Load() != 0 {
					t.Fatal("failed intent invoked receiver/runner")
				}
			case "usage_write":
				if rec.State == sessionstore.JobTerminal || rec.Pending != nil || rec.Recovery == nil || !rec.Recovery.ExecutionStarted {
					t.Fatalf("invented terminal after lost usage: %+v", rec)
				}
			case "unaccepted":
				if rec.State != sessionstore.JobPaymentRejected || calls.Load() != 0 {
					t.Fatalf("fence recovery: %+v", rec)
				}
			default:
				want := uint64(0)
				if scenario == "lost_settlement" || scenario == "terminal_write" {
					want = 42
				}
				if rec.State != sessionstore.JobTerminal || rec.Settlement == "" || rec.WorkUnits != want {
					t.Fatalf("recovery state=%s units=%d want=%d", rec.State, rec.WorkUnits, want)
				}
				if uint64(calls.Load()) != map[bool]uint64{true: 1, false: 0}[want > 0] {
					t.Fatal("runner repeated")
				}
				env := verifiedEnvelope(t, rec.Settlement, s.settlementSigner)
				var signed pb.SettlementRecord
				if err := protojson.Unmarshal(env.Payload, &signed); err != nil {
					t.Fatal(err)
				}
				if signed.GetSettlementSeq() != 1 || signed.GetWholesaleAccountId() != "test-account" || signed.GetActualUnits() != want || signed.GetReservedValueWei() == nil || new(big.Int).SetBytes(signed.GetReservedValueWei().GetValue()).Cmp(big.NewInt(1000000)) != 0 {
					t.Fatalf("signed evidence mismatch: %v", &signed)
				}
				if scenario == "lost_funded_admission" && new(big.Int).SetBytes(signed.GetAccountFundingValueWei().GetValue()).Cmp(big.NewInt(1_000_000_000_000_000)) != 0 {
					t.Fatal("lost funding metadata")
				}
				before := rec.Settlement
				s.sweepPendingDebits(t.Context())
				rec, _ = st.JobByRequestID("real-job")
				if rec.Settlement != before {
					t.Fatal("evidence changed")
				}
				usage, err := client.GetSpendAuthorization(t.Context(), other.Payload.Payer, "auth-real-job", "test-account")
				if err != nil || usage.ActualUnits != want || usage.Billed.Cmp(new(big.Int).SetUint64(want)) != 0 {
					t.Fatalf("receiver usage %+v %v", usage, err)
				}
			}
			account, err := client.GetWholesaleAccount(t.Context(), other.Payload.Payer, "test-account")
			if err != nil || account.Credited.Cmp(big.NewInt(1_000_000_000_000_000)) != 0 {
				t.Fatalf("recovery created funding: %+v %v", account, err)
			}
			beforeCalls := calls.Load()
			for i := 0; i < 2; i++ {
				lookup, err := http.Get(srv.URL + "/v1/exchange/real-job")
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := io.ReadAll(lookup.Body)
				lookup.Body.Close()
				if rec.State == sessionstore.JobTerminal && lookup.Header.Get(livepeerheader.Settlement) != rec.Settlement {
					t.Fatalf("lookup lost signed evidence: %s", raw)
				}
				if scenario == "usage_write" && (lookup.StatusCode != 202 || !bytes.Contains(raw, []byte("ADMITTED_OUTCOME_UNKNOWN"))) {
					t.Fatalf("unknown execution lookup: %s", raw)
				}
				replay, _ := http.NewRequest("POST", srv.URL+"/v1/job", strings.NewReader(`{"messages":[]}`))
				replay.Header = req.Header.Clone()
				res, err := http.DefaultClient.Do(replay)
				if err != nil {
					t.Fatal(err)
				}
				io.Copy(io.Discard, res.Body)
				res.Body.Close()
				if rec.State == sessionstore.JobTerminal && res.Header.Get(livepeerheader.Settlement) != rec.Settlement {
					t.Fatal("replay changed evidence")
				}
				if scenario == "usage_write" && res.StatusCode != 202 {
					t.Fatal("unknown execution replay became terminal")
				}
			}
			if calls.Load() != beforeCalls {
				t.Fatal("lookup/replay repeated execution")
			}
			untouched, err := client.GetSpendAuthorization(t.Context(), other.Payload.Payer, "auth-real-job", "other-account")
			if err != nil || untouched.State != int32(pb.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED) || untouched.Reserved.Cmp(big.NewInt(100)) != 0 || untouched.Billed.Sign() != 0 {
				t.Fatalf("other account changed: %+v %v", untouched, err)
			}
		})
	}
}
func receiverForJob(t *testing.T) (*payment.GRPC, func()) {
	t.Helper()
	binary := os.Getenv("SESSION_RECEIVER_FIXTURE_BIN")
	if binary == "" {
		t.Skip("run make test-revisions for real receiver integration")
	}
	dir, err := os.MkdirTemp("", "revision-rx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	key, _ := crypto.HexToECDSA(jobReceiverKey)
	var cmd *exec.Cmd
	start := func() {
		cmd = exec.Command(binary, "-test.run=^TestSessionReceiverFixture$", "-test.timeout=10m")
		cmd.Env = append(os.Environ(), "SESSION_RECEIVER_FIXTURE_DIR="+dir, "SESSION_RECEIVER_FIXTURE_PAYER="+hex.EncodeToString(crypto.PubkeyToAddress(key.PublicKey).Bytes()))
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(dir, "receiver.sock")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("receiver fixture did not start")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	stop := func() {
		if cmd != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			if err := cmd.Wait(); err != nil {
				t.Errorf("fixture exit: %v", err)
			}
			cmd = nil
		}
	}
	start()
	t.Cleanup(stop)
	client, err := payment.NewGRPC(context.Background(), filepath.Join(dir, "receiver.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Shutdown() })
	return client, func() { stop(); _ = os.Remove(filepath.Join(dir, "receiver.sock")); start() }
}
