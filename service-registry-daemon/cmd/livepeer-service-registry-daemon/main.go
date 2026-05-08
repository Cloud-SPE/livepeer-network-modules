// Binary livepeer-service-registry-daemon implements the registry
// daemon entry point. See docs/operations/running-the-daemon.md for
// the full flag reference.
package main

import (
	"context"
	"fmt"
	"os"
)

// version is stamped at build time via -ldflags.
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "livepeer-service-registry-daemon: "+err.Error())
		os.Exit(1)
	}
}
