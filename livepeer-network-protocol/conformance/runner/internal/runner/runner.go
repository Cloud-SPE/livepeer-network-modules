// Package runner is the runner's main test loop. It loads fixtures, starts
// the in-process mock backend, waits for the target to be ready, runs each
// fixture through its mode driver, and aggregates a Report.
package runner

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/fixtures"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/mockbackend"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/modes"
	"github.com/Cloud-SPE/livepeer-network-rewrite/livepeer-network-protocol/conformance/runner/internal/report"
)

// Config captures everything the runner needs to drive a target.
type Config struct {
	Target       string   // "broker" or "gateway"
	URL          string   // base URL of the target under test
	FixturesPath string   // root of fixtures/ to walk
	Modes        []string // optional mode filter (matches by name or by full mode@vN)
	MockAddr     string   // mock backend listen address; default ":9000"
}

// Run executes the full conformance pass and returns a Report. The caller
// uses Report.ExitCode() to derive the process exit status.
func Run(ctx context.Context, cfg Config) (report.Report, error) {
	if cfg.MockAddr == "" {
		cfg.MockAddr = ":9000"
	}

	// 1. Load fixtures.
	fxs, err := fixtures.LoadAll(cfg.FixturesPath)
	if err != nil {
		return report.Report{}, fmt.Errorf("load fixtures from %q: %w", cfg.FixturesPath, err)
	}
	log.Printf("loaded %d fixtures from %s", len(fxs), cfg.FixturesPath)

	// 2. Filter by --modes if specified.
	if len(cfg.Modes) > 0 {
		fxs = filterModes(fxs, cfg.Modes)
		log.Printf("after --modes filter: %d fixtures", len(fxs))
	}

	// 2b. Filter by target. Fixtures whose Name starts with `gateway-`
	// are gateway-target only; everything else is broker-target.
	fxs = filterByTarget(fxs, modes.Target(cfg.Target))
	log.Printf("after --target filter: %d fixtures", len(fxs))

	// 3. Start mock backend.
	mock := mockbackend.New(cfg.MockAddr)
	go func() {
		if err := mock.Run(); err != nil {
			log.Printf("mock backend error: %v", err)
		}
	}()
	defer func() { _ = mock.Stop() }()

	// 4. Wait briefly for mock backend listener to bind.
	time.Sleep(200 * time.Millisecond)

	// 5. Wait for the target to be ready (max 15s).
	if err := waitForTarget(ctx, cfg.URL); err != nil {
		return report.Report{}, fmt.Errorf("target not ready at %s: %w", cfg.URL, err)
	}
	log.Printf("target ready at %s", cfg.URL)

	// 6. Run each fixture through its mode driver. The driver registry
	// is keyed by target — broker-target drivers send paid requests to
	// the target as if they were a gateway; gateway-target drivers send
	// unpaid customer requests to the target and let the mockbackend
	// (acting as a stand-in upstream broker) verify the gateway-adapter
	// middleware completes the wire shape correctly.
	target := modes.Target(cfg.Target)
	if target == "" {
		target = modes.TargetBroker
	}
	results := make([]report.Result, 0, len(fxs))
	for _, fx := range fxs {
		driver, ok := modes.GetFor(target, fx.Mode)
		if !ok {
			results = append(results, report.Result{
				Name: fx.Name, Mode: fx.Mode, Pass: false,
				Failures: []string{fmt.Sprintf("no driver registered for target=%s mode=%q (registered: %v)",
					target, fx.Mode, modes.NamesFor(target))},
			})
			continue
		}
		results = append(results, driver.Run(ctx, cfg.URL, fx, mock))
	}

	return report.New(results), nil
}

// filterByTarget partitions fixtures by target, using the fixture
// Name's `gateway-` prefix as the role marker. Broker-target ignores
// fixtures with that prefix; gateway-target keeps only fixtures with
// it.
func filterByTarget(fxs []fixtures.Fixture, target modes.Target) []fixtures.Fixture {
	out := make([]fixtures.Fixture, 0, len(fxs))
	for _, fx := range fxs {
		isGateway := strings.HasPrefix(fx.Name, "gateway-")
		if target == modes.TargetGateway {
			if isGateway {
				out = append(out, fx)
			}
			continue
		}
		if !isGateway {
			out = append(out, fx)
		}
	}
	return out
}

// filterModes returns the subset of fxs whose Mode matches any entry in
// modeFilter. A filter entry without "@v" matches by mode-name prefix
// (e.g. "http-reqresp" matches "http-reqresp@v0", "http-reqresp@v1", etc.).
func filterModes(fxs []fixtures.Fixture, modeFilter []string) []fixtures.Fixture {
	out := make([]fixtures.Fixture, 0, len(fxs))
	for _, fx := range fxs {
		if matchesAny(fx.Mode, modeFilter) {
			out = append(out, fx)
		}
	}
	return out
}

func matchesAny(mode string, filters []string) bool {
	for _, f := range filters {
		if mode == f {
			return true
		}
		if !strings.Contains(f, "@v") && strings.HasPrefix(mode, f+"@v") {
			return true
		}
	}
	return false
}

// waitForTarget polls /healthz until 200 or context-deadline.
func waitForTarget(ctx context.Context, baseURL string) error {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout after 15s")
}
