package middleware

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	paymentsv1 "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"github.com/ethereum/go-ethereum/crypto"
	"google.golang.org/protobuf/proto"
)

func stubLookup(capability, offering string) (CapabilitySpec, bool) {
	if capability != "cap" || offering != "off" {
		return CapabilitySpec{}, false
	}
	return CapabilitySpec{WorkUnit: "bytes", PricePerWorkUnitWei: big.NewInt(1), PerUnits: 1}, true
}

type accountPaymentClient struct {
	*payment.Mock
	admitted payment.AdmitAuthorizationRequest
	settled  payment.SettleAuthorizationRequest
}

func (c *accountPaymentClient) FundWholesaleAccount(context.Context, []byte) (*payment.FundWholesaleAccountResult, error) {
	return &payment.FundWholesaleAccountResult{Account: &payment.WholesaleAccount{Payer: bytes20ForBrokerTest(1), Payee: bytes20ForBrokerTest(2), Available: big.NewInt(1000)}, Credited: big.NewInt(100)}, nil
}

func (c *accountPaymentClient) AdmitAuthorization(_ context.Context, req payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	c.admitted = req
	return &payment.AdmitAuthorizationResult{State: int32(paymentsv1.SpendAuthorizationState_SPEND_AUTHORIZATION_ADMITTED), Account: &payment.WholesaleAccount{Payer: bytes20ForBrokerTest(1), Payee: bytes20ForBrokerTest(2), Available: big.NewInt(900)}, Reserved: big.NewInt(100), Credited: new(big.Int)}, nil
}

func (c *accountPaymentClient) SettleAuthorization(_ context.Context, req payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	c.settled = req
	return &payment.SettleAuthorizationResult{State: int32(paymentsv1.SpendAuthorizationState_SPEND_AUTHORIZATION_SETTLED), Account: &payment.WholesaleAccount{Payer: bytes20ForBrokerTest(1), Payee: bytes20ForBrokerTest(2), Available: big.NewInt(970)}, Billed: big.NewInt(30), Released: big.NewInt(70)}, nil
}

func (c *accountPaymentClient) AdvanceAuthorization(context.Context, payment.AdvanceAuthorizationRequest) (*payment.AdvanceAuthorizationResult, error) {
	return nil, nil
}

func (c *accountPaymentClient) GetWholesaleAccount(context.Context, []byte) (*payment.WholesaleAccount, error) {
	return nil, nil
}
func (c *accountPaymentClient) GetSpendAuthorization(context.Context, []byte, string) (*payment.SpendAuthorizationStatus, error) {
	return nil, nil
}

func bytes20ForBrokerTest(v byte) []byte { return bytes.Repeat([]byte{v}, 20) }

func accountJobRequest(t *testing.T, body []byte) *http.Request {
	t.Helper()
	digest := sha256.Sum256(body)
	p := &paymentsv1.SpendAuthorizationPayload{
		Domain: "livepeer-spend-authorization/v1", Payer: bytes20ForBrokerTest(1), Payee: bytes20ForBrokerTest(2),
		ChainId: 42161, Denomination: "wei",
		AuthorizationId: "auth-1", RequestId: "req-1", Protocol: "paid-job/v1", Capability: "cap", Offering: "off", BrokerUri: "https://broker.example",
		AcceptedPrice: &paymentsv1.AcceptedPrice{PricePerUnitWei: &paymentsv1.BigUInt{Value: big.NewInt(1).Bytes()}, UnitsPerPrice: 1, WorkUnitName: "bytes", Capability: "cap", Offering: "off"},
		MaxDebitWei:   &paymentsv1.BigUInt{Value: big.NewInt(100).Bytes()}, MaxTotalUnits: 100, RequestDigest: digest[:],
	}
	wire, err := proto.Marshal(&paymentsv1.SpendAuthorization{Payload: p})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/v1/job", bytes.NewReader(body))
	r.Header.Set(livepeerheader.Capability, "cap")
	r.Header.Set(livepeerheader.Offering, "off")
	r.Header.Set(livepeerheader.Protocol, "paid-job/v1")
	r.Header.Set(livepeerheader.Authorization, base64.StdEncoding.EncodeToString(wire))
	return r.WithContext(context.WithValue(r.Context(), requestIDKey, "req-1"))
}

func TestPaymentAccountAuthorizationReservesAndSettlesActual(t *testing.T) {
	client := &accountPaymentClient{Mock: payment.NewMock()}
	var settlement *paymentsv1.SettlementRecord
	mw := Payment(client, stubLookup, nil, func(in *paymentsv1.SettlementRecord) (string, error) {
		settlement = proto.Clone(in).(*paymentsv1.SettlementRecord)
		return "signed-settlement", nil
	}, "https://broker.example/")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		if string(got) != `{"prompt":"hello"}` {
			t.Fatalf("body changed: %q", got)
		}
		w.Header().Set(livepeerheader.WorkUnits, "30")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, accountJobRequest(t, []byte(`{"prompt":"hello"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(client.admitted.PaymentBytes) != 0 {
		t.Fatal("unexpected top-up payment")
	}
	if client.settled.AuthorizationID != "auth-1" || client.settled.ActualUnits != 30 {
		t.Fatalf("settled=%+v", client.settled)
	}
	if rec.Header().Get(livepeerheader.Settlement) == "" {
		t.Fatal("missing settlement")
	}
	if settlement == nil || new(big.Int).SetBytes(settlement.GetFundedValueWei().GetValue()).Sign() != 0 || new(big.Int).SetBytes(settlement.GetReservedValueWei().GetValue()).Int64() != 100 || new(big.Int).SetBytes(settlement.GetBilledValueWei().GetValue()).Int64() != 30 || new(big.Int).SetBytes(settlement.GetReleasedValueWei().GetValue()).Int64() != 70 {
		t.Fatalf("account settlement conflated funding/reservation/debit: %+v", settlement)
	}
}

func TestPaymentAccountAuthorizationRejectsAlteredBodyBeforeAdmission(t *testing.T) {
	client := &accountPaymentClient{Mock: payment.NewMock()}
	r := accountJobRequest(t, []byte("original"))
	r.Body = io.NopCloser(strings.NewReader("altered"))
	rec := httptest.NewRecorder()
	Payment(client, stubLookup, nil, nil, "https://broker.example")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("backend ran") })).ServeHTTP(rec, r)
	if rec.Code != http.StatusUnauthorized || len(client.admitted.AuthorizationBytes) != 0 {
		t.Fatalf("status=%d admitted=%d", rec.Code, len(client.admitted.AuthorizationBytes))
	}
}

func TestPaymentAccountAuthorizationNeverBillsPastSignedCap(t *testing.T) {
	client := &accountPaymentClient{Mock: payment.NewMock()}
	var settlement *paymentsv1.SettlementRecord
	mw := Payment(client, stubLookup, nil, func(in *paymentsv1.SettlementRecord) (string, error) {
		settlement = proto.Clone(in).(*paymentsv1.SettlementRecord)
		return "signed", nil
	}, "https://broker.example")
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(livepeerheader.WorkUnits, "130")
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, accountJobRequest(t, []byte(`{"prompt":"cap"}`)))
	if client.settled.ActualUnits != 100 {
		t.Fatalf("receiver asked to debit %d units past a 100-unit authorization", client.settled.ActualUnits)
	}
	if settlement == nil || settlement.GetActualUnits() != 130 || settlement.GetBilledUnits() != 100 || settlement.GetOutcome() != paymentsv1.SettlementRecord_STOPPED_AT_BUDGET {
		t.Fatalf("cap settlement=%+v", settlement)
	}
}

func TestValidateCallerProof(t *testing.T) {
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	auth := []byte("single-purpose-authorization")
	digest := crypto.Keccak256(append([]byte("livepeer-invocation-proof/v1\x00"), auth...))
	prefixed := crypto.Keccak256([]byte(fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(digest))), digest)
	sig, err := crypto.Sign(prefixed, key)
	if err != nil {
		t.Fatal(err)
	}
	sig[64] += 27
	proof := base64.StdEncoding.EncodeToString(sig)
	if err := ValidateCallerProof(auth, crypto.FromECDSAPub(&key.PublicKey), proof); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCallerProof(append(auth, 'x'), crypto.FromECDSAPub(&key.PublicKey), proof); err == nil {
		t.Fatal("altered authorization accepted caller proof")
	}
	sig[64] -= 27
	if err := ValidateCallerProof(auth, crypto.FromECDSAPub(&key.PublicKey), base64.StdEncoding.EncodeToString(sig)); err == nil {
		t.Fatal("non-canonical recovery id accepted caller proof")
	}
}

func TestPaymentRejectsPaymentOnlyBeforeProcessingOrWork(t *testing.T) {
	client := &accountPaymentClient{Mock: payment.NewMock()}
	called := false
	req := httptest.NewRequest(http.MethodPost, "/v1/job", strings.NewReader(`{"prompt":"hello"}`))
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString([]byte("funding-ticket")))
	rec := httptest.NewRecorder()
	Payment(client, stubLookup, nil, nil, "https://broker.example")(
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }),
	).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get(livepeerheader.Error) != livepeerheader.ErrAuthorizationRequired {
		t.Fatalf("status=%d error=%q body=%s", rec.Code, rec.Header().Get(livepeerheader.Error), rec.Body.String())
	}
	if called || len(client.admitted.AuthorizationBytes) != 0 {
		t.Fatal("payment-only request reached admission or workload execution")
	}
}
