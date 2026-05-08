// Command layer-check is a stub. The full implementation walks
// `internal/*` packages as a `go vet` analyzer and rejects imports
// that violate the layer-dependency rule from
// `docs/design-docs/architecture.md`. golangci-lint's `depguard`
// covers v1; the analyzer is a stricter follow-up. See
// `docs/exec-plans/tech-debt-tracker.md`.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	_ = root
	fmt.Fprintln(os.Stderr, "layer-check: stub — depguard rules in .golangci.yml cover v1")
}
