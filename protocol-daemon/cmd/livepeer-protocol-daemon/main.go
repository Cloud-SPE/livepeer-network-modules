// livepeer-protocol-daemon is the entry point for the protocol daemon.
//
// Boots either a round-init, reward, or both-mode daemon. Provider wiring,
// preflight, lifecycle coordination, and shutdown live in run.go; this
// file is deliberately tiny so cmd/ doesn't drag the coverage gate down.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// version is stamped at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}
