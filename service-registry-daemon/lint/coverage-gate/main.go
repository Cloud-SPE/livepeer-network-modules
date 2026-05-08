// Command coverage-gate is a stub. The full implementation parses
// `coverage.out` and fails if any non-exempt package falls below 75%
// statement coverage. v1 ships the gate as a no-op so CI is green;
// the real implementation is tracked in tech-debt-tracker.md.
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
	fmt.Fprintln(os.Stderr, "coverage-gate: stub — see docs/exec-plans/tech-debt-tracker.md")
}
