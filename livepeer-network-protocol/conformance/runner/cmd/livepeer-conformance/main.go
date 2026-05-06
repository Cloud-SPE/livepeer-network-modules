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

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpmultipart"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpreqresp"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes/httpstream"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/runner"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	// Register drivers. Each Driver is added to the modes.Registry; the
	// runner dispatches by fixture.Mode.
	modes.Register(httpreqresp.New())
	modes.Register(httpstream.New())
	modes.Register(httpmultipart.New())

	var (
		target       = flag.String("target", "", "broker | gateway")
		url          = flag.String("url", "", "URL of the implementation under test")
		modesFlag    = flag.String("modes", "", "comma-separated mode filter (default: all)")
		fixturesPath = flag.String("fixtures", "/fixtures", "path to fixtures folder")
		mockAddr     = flag.String("mock-addr", ":9000", "address the in-process mock backend binds")
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
	if *target == "gateway" {
		log.Fatalf("--target=gateway is not yet implemented (plan 0008/0009)")
	}

	var modeFilter []string
	if *modesFlag != "" {
		modeFilter = strings.Split(*modesFlag, ",")
	}

	log.Printf("livepeer-conformance %s", version)
	log.Printf("target=%s url=%s fixtures=%s mock=%s registered=%v",
		*target, *url, *fixturesPath, *mockAddr, modes.Names())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
