// livepeer-conformance is the conformance test runner for the
// livepeer-network-protocol spec.
//
// v0.1 status: scaffold only. Flag parsing works; fixture loading and
// assertion logic are TODO. The binary exits with code 2 ("not implemented")
// to make this unambiguous.
//
// See livepeer-network-protocol/conformance/runner/README.md for the planned
// package layout and how to add a mode driver.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

const allModes = "http-reqresp,http-stream,http-multipart,ws-realtime,rtmp-ingress-hls-egress,session-control-plus-media"

func main() {
	var (
		target       = flag.String("target", "", "broker | gateway")
		url          = flag.String("url", "", "URL of the implementation under test")
		modes        = flag.String("modes", allModes, "comma-separated mode list")
		fixturesPath = flag.String("fixtures", "/fixtures", "path to fixtures folder")
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

	modeList := strings.Split(*modes, ",")

	log.Printf("livepeer-conformance %s\n", version)
	log.Printf("target=%s url=%s modes=%v fixtures=%s\n", *target, *url, modeList, *fixturesPath)
	log.Println("scaffold only — fixture loading and assertions are not yet implemented")
	log.Println("see livepeer-network-protocol/conformance/runner/README.md for the planned package layout")

	// Exit non-zero so callers don't mistake the scaffold for a passing test.
	os.Exit(2)
}
