// Command livepeer-conformance runs the executable conformance suite
// for paid-job/v1 and paid-session/v1 against a broker implementation.
//
// Auto mode (default): builds and starts the in-repo reference broker
// with a generated host config pointing at the suite's fake runner and
// backend, runs every scenario, and tears down.
//
// URL mode (--broker-url): the suite starts its fakes, prints their
// addresses, and (with --pause) waits for the operator to configure and
// start their broker-under-test before running the scenarios.
package main

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/scenarios"
)

const configTemplate = `identity:
  orch_eth_address: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
external_base_url: "http://127.0.0.1:%d"
listen:
  paid: "127.0.0.1:%d"
  metrics: "127.0.0.1:%d"
payment_daemon:
  mock: true
session_store:
  path: %q
  sealing_key_file: %q
capabilities:
  - id: conformance:job
    offering_id: all
    protocol: paid-job/v1
    job:
      transports: [unary, stream, multipart]
    health: { initial_status: ready }
    work_unit:
      name: tokens
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:job
    offering_id: unary-only
    protocol: paid-job/v1
    job:
      transports: [unary]
    health: { initial_status: ready }
    work_unit:
      name: tokens
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:job
    offering_id: always-error
    protocol: paid-job/v1
    job:
      transports: [unary]
    health: { initial_status: ready }
    work_unit:
      name: tokens
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: default
    protocol: paid-session/v1
    session:
      descriptor_schema: sfu-room/v1
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: participant_minutes
      extractor: { type: seconds-elapsed }
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
`

func main() {
	var (
		brokerURL = flag.String("broker-url", "", "run against an already-running broker (URL mode)")
		brokerDir = flag.String("broker-dir", defaultBrokerDir(), "path to the reference broker module (auto mode)")
		pause     = flag.Bool("pause", false, "URL mode: wait for Enter after printing fake addresses")
		timeout   = flag.Duration("startup-timeout", 60*time.Second, "auto mode: how long to wait for the broker to become healthy")
	)
	flag.Parse()

	backend := fakes.NewJobBackend()
	defer backend.Close()
	runner := fakes.NewSessionRunner()
	defer runner.Close()

	ctx := &harness.Ctx{
		HTTP:              &http.Client{Timeout: 30 * time.Second},
		Backend:           backend,
		Runner:            runner,
		JobCapability:     "conformance:job",
		JobOfferingAll:    "all",
		JobOfferingUnary:  "unary-only",
		JobOfferingError:  "always-error",
		SessionCapability: "conformance:session",
		SessionOffering:   "default",
		JobUnit:           "tokens",
		SessionUnit:       "participant_minutes",
		RunID:             harness.NewRunID(),
	}

	if *brokerURL != "" {
		fmt.Printf("fake job backend:     %s (error route: %s)\n", backend.URL(), backend.ErrorURL())
		fmt.Printf("fake session runner:  %s (paths: /sessions, /sessions/{id})\n", runner.URL())
		fmt.Println("configure the broker-under-test to serve the offerings in README.md against these addresses.")
		if *pause {
			fmt.Print("press Enter when the broker is ready... ")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		ctx.BrokerURL = *brokerURL
	} else {
		stop, url, err := startReferenceBroker(*brokerDir, backend, runner, *timeout)
		if err != nil {
			fmt.Fprintln(os.Stderr, "start broker:", err)
			os.Exit(2)
		}
		defer stop()
		ctx.BrokerURL = url
	}

	results := harness.RunAll(ctx, scenarios.All(), os.Stdout)
	if !harness.Summarize(results, os.Stdout) {
		os.Exit(1)
	}
}

func defaultBrokerDir() string {
	// conformance/ lives at livepeer-network-protocol/conformance; the
	// reference broker is a sibling of livepeer-network-protocol.
	return filepath.Join("..", "..", "capability-broker")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func startReferenceBroker(brokerDir string, backend *fakes.JobBackend, runner *fakes.SessionRunner, timeout time.Duration) (func(), string, error) {
	paidPort, err := freePort()
	if err != nil {
		return nil, "", err
	}
	metricsPort, err := freePort()
	if err != nil {
		return nil, "", err
	}

	dir, err := os.MkdirTemp("", "livepeer-conformance-*")
	if err != nil {
		return nil, "", err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, "", err
	}
	keyPath := filepath.Join(dir, "seal.key")
	if err := os.WriteFile(keyPath, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, "", err
	}
	cfg := fmt.Sprintf(configTemplate,
		paidPort, paidPort, metricsPort,
		filepath.Join(dir, "state.db"), keyPath,
		backend.URL(), backend.URL(), backend.ErrorURL(), runner.URL())
	cfgPath := filepath.Join(dir, "host-config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return nil, "", err
	}

	cmd := exec.Command("go", "run", "./cmd/livepeer-capability-broker", "--config", cfgPath)
	cmd.Dir = brokerDir
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// Own process group so stop() reaches the broker binary go-run
	// spawns, not just the go-run parent.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, "", fmt.Errorf("start: %w", err)
	}
	stop := func() {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		}
		_, _ = cmd.Process.Wait()
		_ = os.RemoveAll(dir)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", paidPort)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				fmt.Printf("broker under test: %s (reference broker, auto mode)\n\n", url)
				return stop, url, nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	stop()
	return nil, "", fmt.Errorf("broker did not become healthy within %s", timeout)
}
