// coverage-gate enforces a per-package statement-coverage floor.
//
// Reads coverage.out (produced by `go test -coverprofile=coverage.out`),
// walks the repo tree to discover every Go package with source files,
// and exits non-zero if any non-exempt package has < 75% statement
// coverage OR is missing coverage data entirely (no tests).
//
// Exemptions live in lint/coverage-gate/exemptions.txt — one import path per
// line; lines starting with '#' are comments. Every exemption must be
// accompanied by a written reason on the same line after '#'.
//
// Usage:
//
//	go test -coverprofile=coverage.out -covermode=atomic ./...
//	go run ./lint/coverage-gate/... -root .
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const (
	defaultThreshold      = 75.0
	defaultCoverageFile   = "coverage.out"
	defaultExemptionsFile = "lint/coverage-gate/exemptions.txt"
)

func main() {
	var (
		root           = flag.String("root", ".", "repo root containing go.mod")
		coverageFile   = flag.String("coverage", defaultCoverageFile, "path to coverage profile")
		exemptionsFile = flag.String("exemptions", defaultExemptionsFile, "path to exemptions file")
		threshold      = flag.Float64("threshold", defaultThreshold, "minimum coverage percent per package")
	)
	flag.Parse()

	cov, err := os.Open(*coverageFile)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "coverage-gate: open %s: %v\n", *coverageFile, err)
		_, _ = fmt.Fprintln(os.Stderr, "coverage-gate: run `go test -coverprofile=coverage.out ./...` first")
		os.Exit(2)
	}
	defer func() { _ = cov.Close() }()

	var ex io.Reader
	if exFile, err := os.Open(*exemptionsFile); err == nil {
		defer func() { _ = exFile.Close() }()
		ex = exFile
	}

	sourcePkgs, hasTests, err := DiscoverSourcePackages(*root)
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "coverage-gate: discover packages: %v\n", err)
		os.Exit(2)
	}

	os.Exit(Run(cov, ex, sourcePkgs, hasTests, *threshold, os.Stdout, os.Stderr))
}
