package harness

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Scenario is one executable conformance fixture.
type Scenario struct {
	Name string // e.g. "paid-job/unary-exchange"
	Spec string // the normative clause it pins, e.g. "paid-job §7"
	Run  func(c *Ctx) error
}

// Result is a finished scenario.
type Result struct {
	Name    string
	Spec    string
	Status  string // PASS | FAIL | SKIP
	Detail  string
	Elapsed time.Duration
}

// RunAll executes scenarios sequentially and returns their results.
func RunAll(c *Ctx, scenarios []Scenario, log io.Writer) []Result {
	results := make([]Result, 0, len(scenarios))
	for _, sc := range scenarios {
		start := time.Now()
		err := sc.Run(c)
		res := Result{Name: sc.Name, Spec: sc.Spec, Elapsed: time.Since(start)}
		switch {
		case err == nil:
			res.Status = "PASS"
		case errors.Is(err, ErrSkip):
			res.Status = "SKIP"
			res.Detail = strings.TrimPrefix(strings.TrimPrefix(err.Error(), "SKIP"), ": ")
		default:
			res.Status = "FAIL"
			res.Detail = err.Error()
		}
		fmt.Fprintf(log, "%-4s %-52s %8s  %s\n", res.Status, res.Name, res.Elapsed.Round(time.Millisecond), res.Detail)
		results = append(results, res)
	}
	return results
}

// Summarize prints the final report; returns true when nothing failed.
func Summarize(results []Result, log io.Writer) bool {
	var pass, fail, skip int
	for _, r := range results {
		switch r.Status {
		case "PASS":
			pass++
		case "SKIP":
			skip++
		default:
			fail++
		}
	}
	fmt.Fprintf(log, "\n%d passed, %d failed, %d skipped (%d total)\n", pass, fail, skip, len(results))
	if fail > 0 {
		fmt.Fprintln(log, "\nfailed scenarios:")
		for _, r := range results {
			if r.Status == "FAIL" {
				fmt.Fprintf(log, "  FAIL %s (%s)\n       %s\n", r.Name, r.Spec, r.Detail)
			}
		}
	}
	return fail == 0
}
