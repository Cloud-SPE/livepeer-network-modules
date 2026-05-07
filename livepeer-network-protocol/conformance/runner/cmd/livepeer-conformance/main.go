// livepeer-conformance is the conformance test runner for the
// livepeer-network-protocol spec.
//
// v0.1: loads YAML fixtures from --fixtures, runs each through its mode
// driver against a target broker at --url, prints pass/fail, exits 0/1.
//
// Drivers registered in v0.1: http-reqresp@v0. Other modes (http-stream,
// http-multipart, ws-realtime, rtmp-ingress-hls-egress,
// session-control-plus-media) ship their drivers in plan 0006.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/envelope"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/payee"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/gatewaytarget"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpmultipart"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpreqresp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpstream"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/rtmpingresshlsegress"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/sessioncontrolplusmedia"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/wsrealtime"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/runner"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// Register drivers. Each Driver is added to the modes.Registry; the
	// runner dispatches by `(target, fixture.Mode)`.
	modes.Register(httpreqresp.New())
	modes.Register(httpstream.New())
	modes.Register(httpmultipart.New())
	modes.Register(wsrealtime.New())
	modes.Register(rtmpingresshlsegress.New())
	modes.Register(sessioncontrolplusmedia.New())

	// Gateway-target drivers exercise gateway-adapters middleware
	// directly. The non-HTTP modes shipped under plan 0008-followup; the
	// HTTP family is unchanged from plan 0008 (no gateway-target driver
	// needed because the gateway forwards to the broker which is
	// already verified by the broker-target drivers above).
	modes.RegisterFor(modes.TargetGateway, gatewaytarget.NewWSRealtime())
	modes.RegisterFor(modes.TargetGateway, gatewaytarget.NewRTMPIngressHLSEgress())
	modes.RegisterFor(modes.TargetGateway, gatewaytarget.NewSessionControlPlusMedia())

	var (
		target       = flag.String("target", "", "broker | gateway")
		url          = flag.String("url", "", "URL of the implementation under test")
		modesFlag    = flag.String("modes", "", "comma-separated mode filter (default: all)")
		fixturesPath = flag.String("fixtures", "/fixtures", "path to fixtures folder")
		mockAddr     = flag.String("mock-addr", ":9000", "address the in-process mock backend binds")
		payerSocket  = flag.String("payer-socket", "/var/run/livepeer/payer-daemon.sock", "unix socket of the payer-daemon (sender mode)")
		payeeSocket  = flag.String("payee-socket", "", "optional unix socket of the payee-daemon (receiver mode); enables plan 0015 interim-debit assertions via PayeeDaemon.GetBalance")
		showVersion  = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("livepeer-conformance", version)
		return
	}

	if *target == "" || *url == "" {
		log.Println("--target and --url are required")
		log.Println("usage: livepeer-conformance --target=broker --url=http://<impl>:<port> [--modes=...] [--fixtures=...]")
		os.Exit(2)
	}
	if *target != "broker" && *target != "gateway" {
		log.Fatalf("--target must be 'broker' or 'gateway' (got %q)", *target)
	}

	var modeFilter []string
	if *modesFlag != "" {
		modeFilter = strings.Split(*modesFlag, ",")
	}

	log.Printf("livepeer-conformance %s", version)
	log.Printf("target=%s url=%s fixtures=%s mock=%s registered=%v",
		*target, *url, *fixturesPath, *mockAddr, modes.NamesFor(modes.Target(*target)))

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// Dial the payer-daemon. Broker-target drivers call
	// envelope.SubstituteHeaders per fixture, which uses this connection
	// to mint Payment envelopes. Gateway-target drivers don't mint
	// payments (the gateway under test does), so the dial is best-effort
	// in that case — we log a warning instead of dying.
	if err := envelope.Init(ctx, *payerSocket); err != nil {
		if *target == "gateway" {
			log.Printf("payer-daemon dial failed (target=gateway, continuing): %v", err)
		} else {
			log.Fatalf("payer-daemon init: %v", err)
		}
	} else {
		defer envelope.Shutdown()
	}

	// Best-effort dial of the receiver daemon for plan 0015's
	// interim-debit assertions. Empty --payee-socket leaves the client
	// uninitialized; fixtures that need GetBalance will report
	// payee.ErrUnavailable as a fail.
	if *payeeSocket != "" {
		if err := payee.Init(ctx, *payeeSocket); err != nil {
			log.Fatalf("payee-daemon init: %v", err)
		}
		defer payee.Shutdown()
	}

	r, err := runner.Run(ctx, runner.Config{
		Target:       *target,
		URL:          *url,
		FixturesPath: *fixturesPath,
		Modes:        modeFilter,
		MockAddr:     *mockAddr,
	})
	if err != nil {
		log.Fatalf("runner error: %v", err)
	}

	r.Print(os.Stdout)
	os.Exit(r.ExitCode())
}
