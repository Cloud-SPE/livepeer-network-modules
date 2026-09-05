package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
)

// Supplying the runner a broker under test does not have.
//
// A broker in the offer-only grammar advertises nothing until a runner
// attaches: an offer's shape is frozen from what a runner declares, not
// from config. So a stack that starts only a broker has nothing to test
// against, which is why every URL-mode example was red.
//
// Two ways to supply one, and they are NOT interchangeable:
//
//   - --attach-runner attaches the runner inside the process that runs
//     the scenarios. Use this to test a broker binary.
//   - --serve-runner attaches one and serves until stopped, running no
//     scenarios. Use this where something else drives the broker — a
//     smoke script, a hand-driven stack.
//
// The distinction is not cosmetic. Half the session scenarios assert on
// what the RUNNER saw — that the broker terminated a session, that it
// never called the runner on a rejected event. Those assertions read
// the suite's own fakes, so they only mean anything when the fakes the
// broker talks to are the fakes in the asserting process. Split the two
// across containers and every one of them fails, reporting a broker
// fault that is really a topology mistake.

// attachSuiteRunner enrols and attaches the suite's runner, then waits
// for the broker to advertise every offering.
//
// It is deliberately the same runner and the same declarations auto
// mode uses. A separate purpose-built runner would be a second
// definition of what the suite expects a runner to be, and the two
// would drift.
func attachSuiteRunner(brokerURL string, backend *fakes.JobBackend, runner *fakes.SessionRunner,
	jobUnit, sessUnit string, timeout time.Duration) (*harness.Runner, error) {
	if err := waitHealthy(brokerURL, timeout); err != nil {
		return nil, fmt.Errorf("broker never became healthy: %w", err)
	}
	// A per-run host id. The broker's credential store persists, and a
	// host id can only be enrolled once, so a fixed name works exactly
	// once against any broker that keeps its state — the second run
	// fails with host_id_taken and looks like a broker fault.
	credential, hostID, err := enrollAttachCredential(brokerURL, "conformance-runner-"+harness.NewRunID())
	if err != nil {
		return nil, fmt.Errorf("enrol: %w", err)
	}
	offerings := conformanceOfferings(backend, runner, jobUnit, sessUnit)
	specs := make([]harness.RunnerSpec, 0, len(offerings))
	for _, o := range offerings {
		specs = append(specs, o.runnerSpec())
	}
	attached, err := harness.StartRunner(brokerURL, credential, hostID, specs)
	if err != nil {
		return nil, fmt.Errorf("attach: %w", err)
	}
	fmt.Printf("attached as %s with %d capabilities; waiting for the broker to freeze them\n",
		hostID, len(specs))
	// Freezing is certification passing against these fakes. Waiting
	// here rather than letting the first scenario discover it puts the
	// failure next to its cause: a runner that cannot certify is a
	// runner problem, not a scenario problem.
	if err := waitOfferings(brokerURL, len(offerings), timeout); err != nil {
		attached.Close()
		return nil, fmt.Errorf("the broker did not advertise every offering: %w", err)
	}
	fmt.Printf("%d offerings advertised\n\n", len(offerings))
	return attached, nil
}

// serveSuiteRunner attaches the runner and stays up, running no
// scenarios. See the note above on when this is the wrong tool.
func serveSuiteRunner(brokerURL string, backend *fakes.JobBackend, runner *fakes.SessionRunner,
	jobUnit, sessUnit string, timeout time.Duration) int {
	fmt.Printf("fake job backend:     %s (error route: %s)\n", backend.URL(), backend.ErrorURL())
	fmt.Printf("fake session runner:  %s (paths: /sessions, /sessions/{id})\n", runner.URL())
	attached, err := attachSuiteRunner(brokerURL, backend, runner, jobUnit, sessUnit, timeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	defer attached.Close()
	fmt.Println("serving until stopped")

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	fmt.Fprintln(os.Stderr, "stopping")
	return 0
}
