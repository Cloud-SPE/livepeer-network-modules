package server

import (
	"bytes"
	"context"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

func TestAccountHTTPSeparatesSameWalletReservations(t *testing.T) {
	m := payment.NewMock()
	payer, payee := bytes.Repeat([]byte{1}, 20), bytes.Repeat([]byte{2}, 20)
	wire, err := proto.Marshal(&pb.SpendAuthorization{Payload: &pb.SpendAuthorizationPayload{
		MaxDebitWei: &pb.BigUInt{Value: big.NewInt(100).Bytes()}, WholesaleAccountId: "loc-prod", Payer: payer, Payee: payee, AuthorizationId: "loc-request",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.AdmitAuthorization(context.Background(), payment.AdmitAuthorizationRequest{AuthorizationBytes: wire, Reservation: big.NewInt(10)}); err != nil {
		t.Fatal(err)
	}
	for _, account := range []string{"loc-prod", "blueclaw-prod", "loc-dev-laptop"} {
		raw, _ := json.Marshal(map[string]string{"payer_eth_address": "0x0101010101010101010101010101010101010101", "wholesale_account_id": account})
		w := httptest.NewRecorder()
		paymentAccountHandler(m).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/payment/account", bytes.NewReader(raw)))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", account, w.Code, w.Body.String())
		}
		var got struct {
			Account   string `json:"wholesale_account_id"`
			Reserved  string `json:"reserved_value_wei"`
			Isolation uint32 `json:"isolation_version"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := "0"
		if account == "loc-prod" {
			want = "10"
		}
		if got.Account != account || got.Reserved != want || got.Isolation != 1 {
			t.Fatalf("wrong account view: %+v", got)
		}
	}
}

func TestAccountHTTPRequiresExplicitIsolation(t *testing.T) {
	for _, body := range []string{
		`{"payer_eth_address":"0x0101010101010101010101010101010101010101"}`,
		`{"payer_eth_address":"0x0101010101010101010101010101010101010101","wholesale_account_id":"invalid space"}`,
		`{"payer_eth_address":"0x0101010101010101010101010101010101010101","wholesale_account_id":"loc-prod"} {}`,
	} {
		w := httptest.NewRecorder()
		paymentAccountHandler(payment.NewMock()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/payment/account", bytes.NewBufferString(body)))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("accepted invalid account query: %s", body)
		}
	}
}

func TestTicketParamsEchoIsolatedIdentity(t *testing.T) {
	seen := map[string]bool{}
	for _, pair := range [][2]string{{"loc-prod", "payer-a"}, {"loc-prod", "payer-b"}, {"blueclaw-prod", "payer-a"}} {
		raw, _ := json.Marshal(ticketParamsRequestJSON{WholesaleAccountID: pair[0], TicketStreamID: pair[1], SenderETHAddress: "0x0101010101010101010101010101010101010101", RecipientETHAddress: "0x0202020202020202020202020202020202020202", Capability: "opaque", Offering: "default", FaceValueWei: "100"})
		w := httptest.NewRecorder()
		ticketParamsHandler(payment.NewMock()).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/payment/ticket-params", bytes.NewReader(raw)))
		if w.Code != http.StatusOK {
			t.Fatalf("%d: %s", w.Code, w.Body.String())
		}
		var got ticketParamsResponseJSON
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if got.WholesaleAccountID != pair[0] || got.TicketStreamID != pair[1] || got.IsolationVersion != 1 {
			t.Fatalf("identity not relayed: %+v", got)
		}
		if seen[got.TicketParams.RecipientRandHash] {
			t.Fatal("independent account/stream reused ticket generation")
		}
		seen[got.TicketParams.RecipientRandHash] = true
	}
}
