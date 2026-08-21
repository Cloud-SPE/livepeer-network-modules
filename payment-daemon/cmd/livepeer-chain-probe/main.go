// Command livepeer-chain-probe exercises the paid path against a REAL
// chain and asserts on money, not on log lines.
//
// Why this exists: every defect the first Arbitrum One run found was
// invisible to unit tests, conformance, and dev deployments, because all
// three run against a mock payment client. A mock credits what it is
// told to credit, so it cannot show a session that bills zero, a price
// nobody signed, or work served against an empty balance. Those were
// real, and they were only findable here.
//
// It is deliberately NOT part of `make test` or CI. It spends real
// value, it needs real keys, and a check that runs by accident against
// mainnet is worse than no check.
//
// # Cost of a run
//
// Minting a ticket does not move money: a ticket is a signed lottery
// claim. Value moves only when one WINS and the payee redeems it, which
// costs the payee gas and draws the ticket's face value from the payer's
// deposit. At the daemon's defaults (face value 0.001 ETH, win
// probability 1/1024) a probe run costs, in expectation, a fraction of a
// cent — with a 1-in-1024 chance per ticket of actually costing 0.001
// ETH. Small, real, and worth stating before you run it.
//
// # Usage
//
//	livepeer-chain-probe \
//	  --payer-socket=/tmp/payer.sock --payee-socket=/tmp/payee.sock \
//	  --broker-url=http://127.0.0.1:8411 \
//	  --recipient=0x… --protocol=job|session|both
//
// The payer, payee and broker must already be running against the same
// chain. The probe hosts its own fake session runner.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/big"
	"os"
	"time"

	pb "github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/proto-go/livepeer/payments/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type config struct {
	payerSocket string
	payeeSocket string
	brokerURL   string
	recipient   []byte
	capability  string
	offering    string
	workUnit    string
	priceWei    int64
	perUnits    uint64
	fundedWei   *big.Int
	runnerBind  string
	protocol    string
	adminToken  string
}

func main() {
	var (
		payerSocket = flag.String("payer-socket", "/tmp/lpm-payer.sock", "payer daemon UDS")
		payeeSocket = flag.String("payee-socket", "/tmp/lpm-payee.sock", "payee daemon UDS")
		brokerURL   = flag.String("broker-url", "http://127.0.0.1:8411", "broker base URL")
		recipient   = flag.String("recipient", "", "required: payee ETH address (0x-prefixed)")
		capability  = flag.String("capability", "chain:probe", "capability id the broker serves")
		offering    = flag.String("offering", "default", "offering id")
		workUnit    = flag.String("work-unit", "tokens", "the offering's work unit")
		priceWei    = flag.Int64("price-wei", 100, "the offering's amount_wei")
		perUnits    = flag.Uint64("per-units", 1000, "the offering's per_units — keep this above 1: it is the denominator where flooring and ceiling disagree, and a run at 1 cannot see a rounding defect")
		fundedWei   = flag.String("funded-wei", "1000000000000000", "value to authorize per payment")
		runnerBind  = flag.String("runner-bind", "127.0.0.1:0", "address for the probe's fake session runner")
		protocol    = flag.String("protocol", "both", "job | session | both | rotation")
		adminToken  = flag.String("payee-admin-token", "",
			"rotation only: the payee's --payee-admin-token. Rotation is driven through PayeeAdmin.ResetSession, which is closed unless the operator configured a token.")
	)
	flag.Parse()

	addr, err := hexTo20(*recipient)
	if err != nil {
		fatal("--recipient: %v", err)
	}
	funded, ok := new(big.Int).SetString(*fundedWei, 10)
	if !ok || funded.Sign() <= 0 {
		fatal("--funded-wei must be a positive decimal integer")
	}
	cfg := config{
		payerSocket: *payerSocket, payeeSocket: *payeeSocket, brokerURL: *brokerURL,
		recipient: addr, capability: *capability, offering: *offering,
		workUnit: *workUnit, priceWei: *priceWei, perUnits: *perUnits,
		fundedWei: funded, runnerBind: *runnerBind, protocol: *protocol, adminToken: *adminToken,
	}

	payer, closePayer, err := dial(cfg.payerSocket)
	if err != nil {
		fatal("dial payer: %v", err)
	}
	defer closePayer()
	payee, closePayee, err := dial(cfg.payeeSocket)
	if err != nil {
		fatal("dial payee: %v", err)
	}
	defer closePayee()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Printf("chain probe: %s/%s at %d wei per %d %s\n\n",
		cfg.capability, cfg.offering, cfg.priceWei, cfg.perUnits, cfg.workUnit)

	failed := 0
	if cfg.protocol == "job" || cfg.protocol == "both" {
		if err := probeJob(ctx, cfg, pb.NewPayerDaemonClient(payer), pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL paid-job: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS paid-job\n\n")
		}
	}
	if cfg.protocol == "session" || cfg.protocol == "both" {
		if err := probeSession(ctx, cfg, pb.NewPayerDaemonClient(payer), pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL paid-session: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS paid-session\n\n")
		}
	}
	if cfg.protocol == "rotation" {
		if err := probeRotation(ctx, cfg, pb.NewPayerDaemonClient(payer),
			pb.NewPayeeDaemonClient(payee), pb.NewPayeeAdminClient(payee)); err != nil {
			fmt.Printf("FAIL rotation: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS rotation\n\n")
		}
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func dial(socket string) (*grpc.ClientConn, func(), error) {
	conn, err := grpc.NewClient("unix://"+socket, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, err
	}
	return conn, func() { _ = conn.Close() }, nil
}

// mint authorizes one payment against the payer's real deposit.
func mint(ctx context.Context, cfg config, payer pb.PayerDaemonClient, tag string) (*pb.CreatePaymentResponse, error) {
	return payer.CreatePayment(ctx, &pb.CreatePaymentRequest{
		Recipient:           cfg.recipient,
		TicketParamsBaseUrl: cfg.brokerURL,
		MintRequestId:       fmt.Sprintf("chain-probe-%s-%d", tag, time.Now().UnixNano()),
		AcceptedPrice: &pb.AcceptedPrice{
			PricePerUnitWei: &pb.BigUInt{Value: big.NewInt(cfg.priceWei).Bytes()},
			UnitsPerPrice:   cfg.perUnits,
			WorkUnitName:    cfg.workUnit,
			Capability:      cfg.capability,
			Offering:        cfg.offering,
			// A real gateway carries this from the resolver's
			// SelectedRoute; the payer requires it because an unquoted
			// payment has no basis to settle against.
			QuoteRef: &pb.QuoteRef{
				QuoteId: "chain-probe:v1", QuoteVersion: 1,
				ConstraintFingerprint: []byte("chain-probe-constraints"),
				RouteFingerprint:      []byte("chain-probe-route"),
			},
		},
		Funding: &pb.FundingIntent{
			FundedValueWei: &pb.BigUInt{Value: cfg.fundedWei.Bytes()},
			EstimatedUnits: 1000,
		},
	})
}

// billFor is the normative rule from
// livepeer-network-protocol/protocols/offering-axes.md §6.1. The probe
// recomputes it independently rather than importing the broker's copy —
// a checker that shares an implementation with the thing it checks
// cannot catch that implementation being wrong, which is exactly the
// defect this file exists to find.
func billFor(priceWei int64, perUnits, units uint64) *big.Int {
	if perUnits == 0 {
		perUnits = 1
	}
	total := new(big.Int).Mul(big.NewInt(priceWei), new(big.Int).SetUint64(units))
	quo, rem := new(big.Int).QuoRem(total, new(big.Int).SetUint64(perUnits), new(big.Int))
	if rem.Sign() != 0 {
		quo.Add(quo, big.NewInt(1))
	}
	return quo
}

func balanceOf(ctx context.Context, payee pb.PayeeDaemonClient, sender []byte, workID string) (*big.Int, error) {
	r, err := payee.GetBalance(ctx, &pb.GetBalanceRequest{Sender: sender, WorkId: workID})
	if err != nil {
		return nil, err
	}
	return new(big.Int).SetBytes(r.GetBalance()), nil
}

func readAll(r io.Reader) string {
	b, _ := io.ReadAll(r)
	return string(b)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(2)
}

func hexTo20(s string) ([]byte, error) {
	if len(s) >= 2 && (s[:2] == "0x" || s[:2] == "0X") {
		s = s[2:]
	}
	if len(s) != 40 {
		return nil, fmt.Errorf("want a 0x-prefixed 20-byte address, got %d hex chars", len(s))
	}
	out := make([]byte, 20)
	for i := range out {
		var b int
		if _, err := fmt.Sscanf(s[i*2:i*2+2], "%02x", &b); err != nil {
			return nil, err
		}
		out[i] = byte(b)
	}
	return out, nil
}
