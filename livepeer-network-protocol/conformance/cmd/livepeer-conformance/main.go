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
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/scenarios"
	"github.com/ethereum/go-ethereum/crypto"
)

const configTemplate = `identity:
  orch_eth_address: "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
  # The suite runs SIGNED. An unsigned run exercises every rule except
  # the one a clearinghouse actually gates money on, which is the rule
  # most worth grading.
  settlement_key_file: %q
external_base_url: "http://127.0.0.1:%d"
listen:
  paid: "127.0.0.1:%d"
  metrics: "127.0.0.1:%d"
payment_daemon:
  mock: true
  # Durable mock ledger: models the real daemon (which persists to
  # BoltDB) so a restarted session can actually rebind. Without this
  # every restarted session takes the fail-closed branch and the rebind
  # assertions never execute.
  mock_state_path: %q
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
      name: %s
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
      name: %s
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
      name: %s
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:job
    offering_id: slow
    protocol: paid-job/v1
    job:
      transports: [unary]
    health: { initial_status: ready }
    work_unit:
      name: %s
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:job
    offering_id: longstream
    protocol: paid-job/v1
    job:
      transports: [stream]
    health: { initial_status: ready }
    work_unit:
      name: %s
      extractor: { type: openai-usage }
    price: { amount_wei: "1", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:job
    offering_id: fractional
    protocol: paid-job/v1
    job:
      transports: [unary]
    health: { initial_status: ready }
    work_unit:
      name: %s
      extractor: { type: openai-usage }
    # Priced per 1000 units, and deliberately remainder-producing: the
    # fixture backend claims 42 units, so 42 x 100 / 1000 = 4.2 wei.
    # Every offering here used to be per_units 1, which is exactly the
    # denominator at which flooring and ceiling agree — so a rounding
    # defect could not surface.
    price: { amount_wei: "100", per_units: 1000 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: bounded-refill
    protocol: paid-session/v1
    session:
      descriptor_schema: sfu-room/v1
      refill: bounded
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: short-lease
    protocol: paid-session/v1
    session:
      descriptor_schema: sfu-room/v1
      lease_policy: fixed
      lease_max_seconds: 1
      # Heartbeat far away so lease expiry is the trigger under test.
      heartbeat:
        interval_seconds: 2
        missed_threshold: 30
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: rtmp-hls
    protocol: paid-session/v1
    session:
      descriptor_schema: rtmp-hls/v1
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: scope-passthrough
    protocol: paid-session/v1
    session:
      descriptor_schema: scope-passthrough/v1
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: trickle-egress
    protocol: paid-session/v1
    session:
      descriptor_schema: trickle-egress/v1
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
  - id: conformance:session
    offering_id: fast-heartbeat
    protocol: paid-session/v1
    session:
      descriptor_schema: sfu-room/v1
      heartbeat:
        interval_seconds: 1
        missed_threshold: 2
      # A fixed lease keeps funding from preempting the heartbeat, so
      # this offering isolates liveness enforcement.
      lease_policy: fixed
      lease_max_seconds: 600
      runner:
        create_path: /sessions
        status_path: /sessions/{id}
        terminate_path: /sessions/{id}
    health: { initial_status: ready }
    work_unit:
      name: %s
    price: { amount_wei: "10", per_units: 1 }
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
      name: %s
    price: { amount_wei: "10", per_units: 1 }
    backend: { transport: http, url: %q }
`

func main() {
	// os.Exit does not run deferred functions, and this command exits
	// non-zero on a failing suite — which is the common case when you
	// are using it. So every `defer ctl.stop()` was skipped exactly when
	// the suite failed, leaking the reference broker and its temp dir.
	// Four of them were found running 32 hours after their runs ended.
	//
	// run() returns the code and main is the only place that exits.
	os.Exit(run())
}

func run() int {
	var (
		brokerURL = flag.String("broker-url", "", "run against an already-running broker (URL mode)")
		brokerDir = flag.String("broker-dir", defaultBrokerDir(), "path to the reference broker module (auto mode)")
		pause     = flag.Bool("pause", false, "URL mode: wait for Enter after printing fake addresses")
		timeout   = flag.Duration("startup-timeout", 60*time.Second, "auto mode: how long to wait for the broker to become healthy")
		jobUnit   = flag.String("job-unit", "tokens", "work unit the paid-job offerings declare")
		sessUnit  = flag.String("session-unit", "participant_minutes", "work unit the paid-session offerings declare")

		fakesBind = flag.String("fakes-listen", "127.0.0.1",
			"interface the suite's fakes bind; use 0.0.0.0 when the broker under test runs elsewhere on a docker network")
		fakesAdvertise = flag.String("fakes-advertise", "",
			"host the broker under test reaches the fakes on (default: --fakes-listen, or this host's name when binding 0.0.0.0)")
		fakesBackendPort = flag.Int("fakes-backend-port", 0, "fixed port for the fake job backend (0 = ephemeral)")
		fakesRunnerPort  = flag.Int("fakes-runner-port", 0, "fixed port for the fake session runner (0 = ephemeral)")
		warmup           = flag.Duration("warmup", 0,
			"URL mode: wait this long after the fakes are up before running scenarios, so a broker whose health probes have been failing against them recovers")
	)
	flag.Parse()

	listen := fakes.Listen{
		BindHost:      *fakesBind,
		AdvertiseHost: *fakesAdvertise,
		BackendPort:   *fakesBackendPort,
		RunnerPort:    *fakesRunnerPort,
	}
	backend, err := fakes.NewJobBackend(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start fake job backend:", err)
		return 2
	}
	defer backend.Close()
	runner, err := fakes.NewSessionRunner(listen)
	if err != nil {
		fmt.Fprintln(os.Stderr, "start fake session runner:", err)
		return 2
	}
	defer runner.Close()

	ctx := &harness.Ctx{
		HTTP:                      &http.Client{Timeout: 30 * time.Second},
		Backend:                   backend,
		Runner:                    runner,
		JobCapability:             "conformance:job",
		JobOfferingAll:            "all",
		JobOfferingUnary:          "unary-only",
		JobOfferingError:          "always-error",
		JobOfferingSlow:           "slow",
		JobOfferingLongStream:     "longstream",
		JobOfferingFractional:     "fractional",
		SessionOfferingBounded:    "bounded-refill",
		SessionOfferingShortLease: "short-lease",
		SessionOfferingsBySchema: map[string]string{
			"rtmp-hls":          "rtmp-hls",
			"scope-passthrough": "scope-passthrough",
			"trickle-egress":    "trickle-egress",
		},
		SessionCapability:     "conformance:session",
		SessionOffering:       "default",
		SessionOfferingFastHB: "fast-heartbeat",
		JobUnit:               *jobUnit,
		SessionUnit:           *sessUnit,
		RunID:                 harness.NewRunID(),
	}

	if *brokerURL != "" {
		fmt.Printf("fake job backend:     %s (error route: %s)\n", backend.URL(), backend.ErrorURL())
		fmt.Printf("fake session runner:  %s (paths: /sessions, /sessions/{id})\n", runner.URL())
		fmt.Println("configure the broker-under-test to serve the offerings in README.md against these addresses.")
		if *pause {
			fmt.Print("press Enter when the broker is ready... ")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		if *warmup > 0 {
			// A broker that was already running has been probing dead
			// addresses; its backends stay unselectable until a couple
			// of probe cycles land on the now-live fakes.
			fmt.Printf("warmup: waiting %s for the broker's health probes to see the fakes\n\n", *warmup)
			time.Sleep(*warmup)
		}
		ctx.BrokerURL = *brokerURL
	} else {
		ctl, url, err := startReferenceBroker(*brokerDir, backend, runner, *timeout, *jobUnit, *sessUnit)
		if err != nil {
			fmt.Fprintln(os.Stderr, "start broker:", err)
			return 2
		}
		// A signal bypasses defers too, and Ctrl-C on a long suite is
		// how an impatient operator ends most runs. Stop the broker on
		// the way out rather than leaving it holding a port and a temp
		// dir nobody will ever look for.
		stopOnce := sync.OnceFunc(ctl.stop)
		defer stopOnce()
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-sigCh
			fmt.Fprintln(os.Stderr, "\ninterrupted — stopping the reference broker")
			stopOnce()
			os.Exit(130)
		}()
		ctx.BrokerURL = url
		// Auto mode owns the process, so restart-dependent scenarios
		// can run for real instead of skipping.
		ctx.RestartBroker = ctl.restart
		ctx.RestartBrokerLosingPayment = ctl.restartLosingPayment
		// Only an auto-launched broker has a key this suite chose, so
		// only then can a signature be checked against an expected
		// address. Against an external broker the signature scenarios
		// skip rather than assert something they cannot know.
		ctx.SettlementSigner = ctl.settlementSigner
	}

	results := harness.RunAll(ctx, scenarios.All(), os.Stdout)
	if !harness.Summarize(results, os.Stdout) {
		return 1
	}
	return 0
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

func startReferenceBroker(brokerDir string, backend *fakes.JobBackend, runner *fakes.SessionRunner,
	timeout time.Duration, jobUnit, sessUnit string) (*brokerControl, string, error) {
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

	// A delegated settlement key, generated per run. The address it
	// derives to is what every settlement in the run must recover to —
	// the assertion a clearinghouse makes before it books anything.
	settleKey, err := crypto.GenerateKey()
	if err != nil {
		return nil, "", err
	}
	settleKeyPath := filepath.Join(dir, "settlement.key")
	if err := os.WriteFile(settleKeyPath,
		[]byte(hex.EncodeToString(crypto.FromECDSA(settleKey))), 0o600); err != nil {
		return nil, "", err
	}
	signerAddr := crypto.PubkeyToAddress(settleKey.PublicKey).Hex()

	cfg := fmt.Sprintf(configTemplate,
		settleKeyPath,
		paidPort, paidPort, metricsPort,
		filepath.Join(dir, "payment-mock.json"),
		filepath.Join(dir, "state.db"), keyPath,
		jobUnit, backend.URL(),
		jobUnit, backend.URL(),
		jobUnit, backend.ErrorURL(),
		jobUnit, backend.SlowURL(),
		jobUnit, backend.LongStreamURL(),
		jobUnit, backend.URL(), // fractional
		sessUnit, runner.URL(), // bounded-refill
		sessUnit, runner.URL(), // short-lease
		sessUnit, runner.URL(), // rtmp-hls
		sessUnit, runner.URL(), // scope-passthrough
		sessUnit, runner.URL(), // trickle-egress
		sessUnit, runner.URL(), // fast-heartbeat
		sessUnit, runner.URL()) // default
	cfgPath := filepath.Join(dir, "host-config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		return nil, "", err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", paidPort)
	var cur *exec.Cmd

	launch := func() error {
		cmd := exec.Command("go", "run", "./cmd/livepeer-capability-broker", "--config", cfgPath)
		cmd.Dir = brokerDir
		cmd.Stdout = os.Stderr
		cmd.Stderr = os.Stderr
		// Own process group so kills reach the broker binary go-run
		// spawns, not just the go-run parent.
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start: %w", err)
		}
		cur = cmd
		return waitHealthy(url, timeout)
	}
	halt := func() {
		if cur != nil && cur.Process != nil {
			_ = syscall.Kill(-cur.Process.Pid, syscall.SIGTERM)
			_, _ = cur.Process.Wait()
		}
		cur = nil
	}

	if err := launch(); err != nil {
		halt()
		return nil, "", err
	}
	fmt.Printf("broker under test: %s (reference broker, auto mode)\n\n", url)

	ctl := &brokerControl{
		settlementSigner: signerAddr,
		stop:             func() { halt(); _ = os.RemoveAll(dir) },
		restart: func() error {
			halt()
			// The state store must survive: same dir, same key, same
			// config — only the process is replaced.
			return launch()
		},
		restartLosingPayment: func() error {
			halt()
			// Wipe only the payment ledger: the broker's own session
			// store survives, so this is precisely "the runner still
			// has it, the payment layer does not" — the case §9.2's
			// terminal branch exists for.
			_ = os.Remove(filepath.Join(dir, "payment-mock.json"))
			return launch()
		},
	}
	return ctl, url, nil
}

// brokerControl is the runner's handle on a broker it owns.
type brokerControl struct {
	stop                 func()
	restart              func() error
	restartLosingPayment func() error
	// settlementSigner is the eth address of the delegated key this run
	// gave the broker. Every settlement it emits must recover to it.
	settlementSigner string
}

func waitHealthy(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("broker did not become healthy within %s", timeout)
}
