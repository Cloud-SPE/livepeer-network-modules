package metrics

import (
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNoopHandlerReturns404(t *testing.T) {
	rec := NewNoop()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("noop handler: want 404, got %d", resp.StatusCode)
	}
}

func TestPrometheusExposesNamespacedMetrics(t *testing.T) {
	p := NewPrometheus()
	p.SetBuildInfo("v1.2.3", "receiver", "go1.99")
	p.IncGRPCRequest(RoleReceiver, "ProcessPayment", "OK")
	p.IncTicket(TicketAccepted)
	p.IncTicketRejected(ReasonNonceReplay)
	p.IncWinningTicket()
	p.AddCreditedEVGwei(WeiToGwei(big.NewInt(2_000_000_000))) // 2 gwei
	p.IncRedemption(RedeemRedeemed)
	p.SetGasPriceWei(WeiToFloat(big.NewInt(150)))
	p.SetCurrentRound(4242)

	srv := httptest.NewServer(p.Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 1<<16)
	n, _ := resp.Body.Read(buf)
	body := string(buf[:n])

	for _, want := range []string{
		`livepeer_payment_grpc_requests_total{code="OK",method="ProcessPayment",role="receiver"} 1`,
		`livepeer_payment_tickets_total{result="accepted"} 1`,
		`livepeer_payment_tickets_rejected_total{reason="nonce_replay"} 1`,
		`livepeer_payment_winning_tickets_total 1`,
		`livepeer_payment_redemptions_total{result="redeemed"} 1`,
		`livepeer_payment_current_round 4242`,
		`livepeer_payment_build_info{go_version="go1.99",mode="receiver",version="v1.2.3"} 1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition missing %q", want)
		}
	}
	// Standard collectors come for free.
	if !strings.Contains(body, "go_goroutines") {
		t.Error("expected go_* collector output")
	}
}

func TestWeiToGwei(t *testing.T) {
	if got := WeiToGwei(big.NewInt(1_500_000_000)); got != 1.5 {
		t.Fatalf("WeiToGwei: want 1.5, got %v", got)
	}
	if got := WeiToGwei(nil); got != 0 {
		t.Fatalf("WeiToGwei(nil): want 0, got %v", got)
	}
}

func TestUnsetLabel(t *testing.T) {
	if unset("") != LabelUnset {
		t.Fatal("empty should map to LabelUnset")
	}
	if unset("x") != "x" {
		t.Fatal("non-empty should pass through")
	}
}
