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
//	  --recipient=0x… --protocol=wholesale
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
	payerSocket             string
	payeeSocket             string
	brokerURL               string
	brokerURI               string
	recipient               []byte
	capability              string
	offering                string
	workUnit                string
	priceWei                int64
	perUnits                uint64
	protocol                string
	chainID                 uint64
	accountFloat            *big.Int
	maxAuthUnits            uint64
	sessionCapability       string
	sessionOffering         string
	sessionWorkUnit         string
	sessionPriceWei         int64
	sessionPerUnits         uint64
	sessionMaxAuthUnits     uint64
	sessionRunnerControlURL string
	checkpointFile          string
	checkpointDir           string
}

func main() {
	var (
		payerSocket             = flag.String("payer-socket", "/tmp/lpm-payer.sock", "payer daemon UDS")
		payeeSocket             = flag.String("payee-socket", "/tmp/lpm-payee.sock", "payee daemon UDS")
		brokerURL               = flag.String("broker-url", "http://127.0.0.1:8411", "broker base URL")
		brokerURI               = flag.String("broker-uri", "", "wholesale only: externally advertised broker origin signed into authorizations (defaults to --broker-url)")
		recipient               = flag.String("recipient", "", "required: payee ETH address (0x-prefixed)")
		capability              = flag.String("capability", "chain:probe", "capability id the broker serves")
		offering                = flag.String("offering", "default", "offering id")
		workUnit                = flag.String("work-unit", "tokens", "the offering's work unit")
		priceWei                = flag.Int64("price-wei", 100, "the offering's amount_wei")
		perUnits                = flag.Uint64("per-units", 1000, "the offering's per_units — keep this above 1: it is the denominator where flooring and ceiling disagree, and a run at 1 cannot see a rounding defect")
		protocol                = flag.String("protocol", "wholesale", "wholesale | wholesale-recovery-prepare | wholesale-recovery-verify | wholesale-evidence")
		chainID                 = flag.Uint64("chain-id", 42161, "chain id signed into wholesale spend authorizations")
		accountFloat            = flag.String("account-float-wei", "", "wholesale only: target available account float; defaults to the maximum authorization debit")
		maxAuthUnits            = flag.Uint64("max-authorization-units", 131072, "wholesale only: maximum units bound into each single-purpose authorization")
		sessionCapability       = flag.String("session-capability", "conformance:session", "wholesale only: paid-session capability id")
		sessionOffering         = flag.String("session-offering", "default", "wholesale only: paid-session offering id")
		sessionWorkUnit         = flag.String("session-work-unit", "participant_minutes", "wholesale only: paid-session work unit")
		sessionPriceWei         = flag.Int64("session-price-wei", 100, "wholesale only: paid-session price amount in wei")
		sessionPerUnits         = flag.Uint64("session-per-units", 1, "wholesale only: paid-session units per quoted price")
		sessionMaxAuthUnits     = flag.Uint64("session-max-authorization-units", 600, "wholesale only: paid-session cumulative unit ceiling")
		sessionRunnerControlURL = flag.String("session-runner-control-url", "http://runner:8092", "wholesale only: conformance session runner's internal probe-control URL")
		checkpointFile          = flag.String("checkpoint-file", "", "wholesale recovery only: durable checkpoint path on a persistent volume")
		checkpointDir           = flag.String("checkpoint-dir", "/var/lib/livepeer/payment-daemon", "wholesale evidence only: directory containing recovery checkpoints")
	)
	flag.Parse()

	addr, err := hexTo20(*recipient)
	if err != nil {
		fatal("--recipient: %v", err)
	}
	if *protocol != "wholesale" && *protocol != "wholesale-recovery-prepare" && *protocol != "wholesale-recovery-verify" && *protocol != "wholesale-evidence" {
		fatal("--protocol must be wholesale, wholesale-recovery-prepare, wholesale-recovery-verify, or wholesale-evidence")
	}
	var ok bool
	var targetFloat *big.Int
	if *accountFloat != "" {
		targetFloat, ok = new(big.Int).SetString(*accountFloat, 10)
		if !ok || targetFloat.Sign() <= 0 {
			fatal("--account-float-wei must be a positive decimal integer")
		}
	}
	cfg := config{
		payerSocket: *payerSocket, payeeSocket: *payeeSocket, brokerURL: *brokerURL,
		brokerURI: *brokerURI,
		recipient: addr, capability: *capability, offering: *offering,
		workUnit: *workUnit, priceWei: *priceWei, perUnits: *perUnits,
		protocol: *protocol,
		chainID:  *chainID, accountFloat: targetFloat, maxAuthUnits: *maxAuthUnits,
		sessionCapability: *sessionCapability, sessionOffering: *sessionOffering,
		sessionWorkUnit: *sessionWorkUnit, sessionPriceWei: *sessionPriceWei,
		sessionPerUnits: *sessionPerUnits, sessionMaxAuthUnits: *sessionMaxAuthUnits,
		sessionRunnerControlURL: *sessionRunnerControlURL, checkpointFile: *checkpointFile, checkpointDir: *checkpointDir,
	}
	if cfg.brokerURI == "" {
		cfg.brokerURI = cfg.brokerURL
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
	if cfg.protocol == "wholesale" {
		if err := probeWholesale(ctx, cfg, pb.NewPayerDaemonClient(payer), pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL wholesale account: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS wholesale account\n\n")
		}
	}
	if cfg.protocol == "wholesale-recovery-prepare" {
		if err := prepareWholesaleRecovery(ctx, cfg, pb.NewPayerDaemonClient(payer), pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL wholesale recovery prepare: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS wholesale recovery prepare\n\n")
		}
	}
	if cfg.protocol == "wholesale-recovery-verify" {
		if err := verifyWholesaleRecovery(ctx, cfg, pb.NewPayerDaemonClient(payer), pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL wholesale recovery verify: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS wholesale recovery verify\n\n")
		}
	}
	if cfg.protocol == "wholesale-evidence" {
		if err := probeWholesaleEvidence(ctx, cfg, pb.NewPayeeDaemonClient(payee)); err != nil {
			fmt.Printf("FAIL wholesale evidence: %v\n\n", err)
			failed++
		} else {
			fmt.Print("PASS wholesale evidence\n\n")
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
