// Package report formats runner pass/fail output and computes the exit code.
package report

import (
	"fmt"
	"io"
)

// Result is one fixture's outcome.
type Result struct {
	Name     string
	Mode     string
	Pass     bool
	Failures []string
}

// Report aggregates many fixture results.
type Report struct {
	Results []Result
	Passed  int
	Failed  int
}

// New aggregates results into a Report, computing Passed/Failed counts.
func New(results []Result) Report {
	r := Report{Results: results}
	for _, res := range results {
		if res.Pass {
			r.Passed++
		} else {
			r.Failed++
		}
	}
	return r
}

// Print writes a human-readable summary to w.
func (r Report) Print(w io.Writer) {
	for _, res := range r.Results {
		if res.Pass {
			fmt.Fprintf(w, "  PASS: %s [%s]\n", res.Name, res.Mode)
		} else {
			fmt.Fprintf(w, "  FAIL: %s [%s]\n", res.Name, res.Mode)
			for _, f := range res.Failures {
				fmt.Fprintf(w, "        - %s\n", f)
			}
		}
	}
	fmt.Fprintf(w, "\nresult: %d passed, %d failed\n", r.Passed, r.Failed)
}

// ExitCode returns 0 if all fixtures passed, 1 otherwise.
func (r Report) ExitCode() int {
	if r.Failed > 0 {
		return 1
	}
	return 0
}
