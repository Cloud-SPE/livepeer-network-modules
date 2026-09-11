package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/livepeerheader"
	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/payment"
	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/protobuf/proto"
)

type fundingAccountClient struct {
	payment.Client
	fundCalls atomic.Int64
	wantWire  []byte
}

func (f *fundingAccountClient) FundWholesaleAccount(_ context.Context, wire []byte) (*payment.FundWholesaleAccountResult, error) {
	f.fundCalls.Add(1)
	f.wantWire = append([]byte(nil), wire...)
	return &payment.FundWholesaleAccountResult{
		Account: &payment.WholesaleAccount{
			Payer:     bytesOf(0xab, 20),
			Payee:     bytesOf(0xcd, 20),
			Available: big.NewInt(37),
			Version:   4,
		},
		Credited: big.NewInt(37),
	}, nil
}

func (f *fundingAccountClient) AdmitAuthorization(context.Context, payment.AdmitAuthorizationRequest) (*payment.AdmitAuthorizationResult, error) {
	panic("not called")
}

func (f *fundingAccountClient) AdvanceAuthorization(context.Context, payment.AdvanceAuthorizationRequest) (*payment.AdvanceAuthorizationResult, error) {
	panic("not called")
}

func (f *fundingAccountClient) SettleAuthorization(context.Context, payment.SettleAuthorizationRequest) (*payment.SettleAuthorizationResult, error) {
	panic("not called")
}

func (f *fundingAccountClient) GetWholesaleAccount(context.Context, []byte) (*payment.WholesaleAccount, error) {
	panic("not called")
}

func (f *fundingAccountClient) GetSpendAuthorization(context.Context, []byte, string) (*payment.SpendAuthorizationStatus, error) {
	panic("not called")
}

func TestFundWholesaleAccountAcceptsValueWithoutSpendAuthority(t *testing.T) {
	client := &fundingAccountClient{Client: payment.NewMock()}
	srv, _ := newJobOfferBroker(t, nil, client, "")

	wire, err := proto.Marshal(&pb.Payment{
		TicketParams: &pb.TicketParams{RecipientRandHash: bytesOf(0x44, 32)},
		Sender:       bytesOf(0xab, 20),
		ExpectedPrice: &pb.PriceInfo{
			PricePerUnit:  1,
			PixelsPerUnit: 1,
			Constraint: "cap=openai%3Achat-completions;off=default;wu=tokens;est=37;" +
				"qid=quote-1;qv=1;cfp=aa;rfp=bb",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/payment/account/fund", strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(livepeerheader.Capability, "openai:chat-completions")
	req.Header.Set(livepeerheader.Offering, "default")
	req.Header.Set(livepeerheader.Payment, base64.StdEncoding.EncodeToString(wire))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	if client.fundCalls.Load() != 1 || !bytes.Equal(client.wantWire, wire) {
		t.Fatalf("fund call was not relayed exactly once")
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got["credited_value_wei"] != "37" || got["available_value_wei"] != "37" || got["account_version"] != float64(4) {
		t.Fatalf("response = %v", got)
	}
}

func bytesOf(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}
