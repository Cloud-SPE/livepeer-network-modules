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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/fakes"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/harness"
	"github.com/Cloud-SPE/livepeer-network-modules/livepeer-network-protocol/conformance/internal/scenarios"
	"github.com/ethereum/go-ethereum/crypto"
)

// The reference broker's config, in the offer-only grammar. Runner
// facts are deliberately absent: the suite's own runner declares them at
// attach, which is the path a real deployment uses and therefore the one
// worth grading (plan 0043).
const configHeader = `identity:
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
# The suite enrols its own runner over the admin API once the broker is
# healthy.
admin_auth:
  method: bearer
  secret_ref: env://BROKER_ADMIN_TOKEN
credential_store:
  path: %q
  sealing_key_file: %q
offers_state_path: %q
offers:
`

// offering is one conformance offering. The offer YAML the broker reads
// and the runner spec the suite attaches with are both generated from
// this, so the two cannot drift — which is the failure the old
// hand-maintained pair invited.
type offering struct {
	capabilityID     string
	offeringID       string
	protocol         string
	transports       []string // paid-job
	descriptorSchema string   // paid-session
	workUnit         string
	extractor        map[string]any
	paths            map[string]string
	baseURL          string
	priceWei         string
	perUnits         int
	sessionPolicy    string // YAML fragment, indented four spaces
}

func (o offering) offerYAML() string {
	var b strings.Builder
	fmt.Fprintf(&b, "  - offering_id: %s\n", o.offeringID)
	fmt.Fprintf(&b, "    capability: %s\n", o.capabilityID)
	fmt.Fprintf(&b, "    protocol: %s\n", o.protocol)
	// Each offering selects exactly its own runner entry, so the
	// scenarios keep the per-offering behaviour they assert.
	fmt.Fprintf(&b, "    match: { identity.variant: %s }\n", o.offeringID)
	fmt.Fprintf(&b, "    price: { amount_wei: %q, per_units: %d }\n", o.priceWei, o.perUnits)
	if o.sessionPolicy != "" {
		b.WriteString("    session_policy:\n")
		b.WriteString(o.sessionPolicy)
	}
	return b.String()
}

func (o offering) runnerSpec() harness.RunnerSpec {
	spec := harness.RunnerSpec{
		LocalID:        o.offeringID,
		CapabilityID:   o.capabilityID,
		Protocol:       o.protocol,
		Identity:       map[string]string{"variant": o.offeringID},
		WorkUnitName:   o.workUnit,
		Extractor:      o.extractor,
		Paths:          o.paths,
		BaseURL:        o.baseURL,
		SchemaVersions: map[string]string{o.protocol: "1.0.0"},
	}
	if o.descriptorSchema != "" {
		spec.DescriptorSchemas = []string{o.descriptorSchema}
		spec.Metering = "runner-reported"
		spec.SchemaVersions[o.descriptorSchema] = "1.0.0"
	} else {
		spec.Transports = o.transports
	}
	return spec
}

// conformanceOfferings is the single list both halves are built from.
func conformanceOfferings(backend *fakes.JobBackend, runner *fakes.SessionRunner, jobUnit, sessUnit string) []offering {
	openaiUsage := map[string]any{"type": "openai-usage"}
	jobPaths := func(path string) map[string]string { return map[string]string{"invoke": path} }
	sessionPaths := map[string]string{
		"create": "/sessions", "status": "/sessions/{id}", "terminate": "/sessions/{id}",
	}
	job := func(id string, transports []string, path, price string, perUnits int) offering {
		return offering{
			capabilityID: "conformance:job", offeringID: id, protocol: "paid-job/v1",
			transports: transports, workUnit: jobUnit, extractor: openaiUsage,
			paths: jobPaths(path), baseURL: backend.URL(), priceWei: price, perUnits: perUnits,
		}
	}
	session := func(id, schema, policy string) offering {
		return offering{
			capabilityID: "conformance:session", offeringID: id, protocol: "paid-session/v1",
			descriptorSchema: schema, workUnit: sessUnit, paths: sessionPaths,
			baseURL: runner.URL(), priceWei: "10", perUnits: 1, sessionPolicy: policy,
		}
	}
	fixedLease := "      lease_policy: fixed\n      lease_max_seconds: 600\n"
	return []offering{
		job("all", []string{"unary", "stream", "multipart"}, "/", "1", 1),
		job("unary-only", []string{"unary"}, "/", "1", 1),
		// The error, slow and longstream routes are paths on the same
		// fake: the RUNNER declares which one it serves, which is how a
		// real runner points at its own endpoint.
		job("always-error", []string{"unary"}, "/error", "1", 1),
		job("slow", []string{"unary"}, "/slow", "1", 1),
		job("longstream", []string{"stream"}, "/longstream", "1", 1),
		// Priced per many units, so the paid path is exercised at a
		// denominator where flooring and ceiling disagree.
		job("fractional", []string{"unary"}, "/", "100", 1000),
		session("bounded-refill", "sfu-room/v1", "      refill: bounded\n"+fixedLease),
		// Heartbeat far away so lease expiry is the trigger under test.
		session("short-lease", "sfu-room/v1",
			"      lease_policy: fixed\n      lease_max_seconds: 1\n      heartbeat: { interval_seconds: 2, missed_threshold: 30 }\n"),
		session("rtmp-hls", "rtmp-hls/v1", fixedLease),
		session("scope-passthrough", "scope-passthrough/v1", fixedLease),
		session("trickle-egress", "trickle-egress/v1", fixedLease),
		// A fixed lease keeps funding from preempting the heartbeat, so
		// this offering isolates liveness enforcement.
		session("fast-heartbeat", "sfu-room/v1",
			"      heartbeat: { interval_seconds: 1, missed_threshold: 2 }\n"+fixedLease),
		session("default", "sfu-room/v1", ""),
	}
}

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
		brokerURL  = flag.String("broker-url", "", "run against an already-running broker (URL mode)")
		brokerDir  = flag.String("broker-dir", defaultBrokerDir(), "path to the reference broker module (auto mode)")
		pause      = flag.Bool("pause", false, "URL mode: wait for Enter after printing fake addresses")
		timeout    = flag.Duration("startup-timeout", 60*time.Second, "auto mode: how long to wait for the broker to become healthy")
		jobUnit    = flag.String("job-unit", "tokens", "work unit the paid-job offerings declare")
		sessUnit   = flag.String("session-unit", "participant_minutes", "work unit the paid-session offerings declare")
		attachCred = flag.String("attach-credential", "", "bearer credential enrolled on the broker for the runner-attach scenarios (empty: they skip)")
		attachHost = flag.String("attach-host-id", "conformance-runner", "host_id the attach credential was enrolled for")

		fakesBind = flag.String("fakes-listen", "127.0.0.1",
			"interface the suite's fakes bind; use 0.0.0.0 when the broker under test runs elsewhere on a docker network")
		fakesAdvertise = flag.String("fakes-advertise", "",
			"host the broker under test reaches the fakes on (default: --fakes-listen, or this host's name when binding 0.0.0.0)")
		fakesBackendPort = flag.Int("fakes-backend-port", 0, "fixed port for the fake job backend (0 = ephemeral)")
		fakesRunnerPort  = flag.Int("fakes-runner-port", 0, "fixed port for the fake session runner (0 = ephemeral)")
		warmup           = flag.Duration("warmup", 0,
			"URL mode: wait this long after the fakes are up before running scenarios, so a broker whose health probes have been failing against them recovers")
		serveRunner = flag.Bool("serve-runner", false,
			"attach the suite's runner to --broker-url and stay up serving it, instead of running scenarios")
		attachRunner = flag.Bool("attach-runner", false,
			"URL mode: attach the suite's own runner to --broker-url before running scenarios, for a broker that has none of its own")
		settlementSigner = flag.String("settlement-signer", "",
			"URL mode: eth address of the broker's delegated settlement key, so the settlement-signature scenarios run instead of skipping")
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
		AttachCredential:      *attachCred,
		AttachHostID:          *attachHost,
	}

	if *serveRunner {
		if *brokerURL == "" {
			fmt.Fprintln(os.Stderr, "--serve-runner needs --broker-url: it attaches to a broker, it does not start one")
			return 2
		}
		return serveSuiteRunner(*brokerURL, backend, runner, *jobUnit, *sessUnit, *timeout)
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
		// The broker does not publish its delegated settlement key on
		// any unauthenticated surface, so URL mode has to be told. Left
		// empty the two signature scenarios skip, saying they are
		// skipping — which is better than passing without checking.
		ctx.SettlementSigner = *settlementSigner
		if *attachRunner {
			attached, err := attachSuiteRunner(*brokerURL, backend, runner, *jobUnit, *sessUnit, *timeout)
			if err != nil {
				fmt.Fprintln(os.Stderr, "attach the suite's runner:", err)
				return 2
			}
			defer attached.Close()
			// The attach scenarios need a credential of their own.
			// Reusing the serving runner's would have them revoking and
			// disconnecting the runner every other scenario depends on.
			if ctx.AttachCredential == "" {
				if c, h, err := enrollAttachCredential(*brokerURL, "conformance-attach-"+ctx.RunID); err == nil {
					ctx.AttachCredential, ctx.AttachHostID = c, h
				} else {
					fmt.Fprintf(os.Stderr, "attach enrollment unavailable (%v); attach scenarios will skip\n", err)
				}
			}
		}
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
		if cred, hostID, err := enrollAttachCredential(url, "conformance-runner"); err != nil {
			// Not fatal: the attach scenarios skip with the reason, and
			// every paid-path scenario still runs.
			fmt.Fprintf(os.Stderr, "attach enrollment unavailable (%v); attach scenarios will skip\n", err)
		} else {
			ctx.AttachCredential, ctx.AttachHostID = cred, hostID
		}
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

	if err := os.Setenv("BROKER_ADMIN_TOKEN", conformanceAdminToken); err != nil {
		return nil, "", err
	}
	offerings := conformanceOfferings(backend, runner, jobUnit, sessUnit)
	var cfg strings.Builder
	fmt.Fprintf(&cfg, configHeader,
		settleKeyPath,
		paidPort, paidPort, metricsPort,
		filepath.Join(dir, "payment-mock.json"),
		filepath.Join(dir, "state.db"), keyPath,
		filepath.Join(dir, "credentials.db"), keyPath,
		filepath.Join(dir, "offers.db"))
	for _, o := range offerings {
		cfg.WriteString(o.offerYAML())
	}
	cfgPath := filepath.Join(dir, "host-config.yaml")
	if err := os.WriteFile(cfgPath, []byte(cfg.String()), 0o600); err != nil {
		return nil, "", err
	}

	url := fmt.Sprintf("http://127.0.0.1:%d", paidPort)
	var cur *exec.Cmd

	// The suite's own runner. It attaches over the wire and declares
	// every offering, so the offers the scenarios exercise are frozen
	// by the same path a real deployment uses. A restart drops the
	// tunnel, so each launch re-attaches.
	specs := make([]harness.RunnerSpec, 0, len(offerings))
	for _, o := range offerings {
		specs = append(specs, o.runnerSpec())
	}
	var suiteRunner *harness.Runner
	suiteCred, suiteHost := "", ""

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
		if err := waitHealthy(url, timeout); err != nil {
			return err
		}
		// The credential store is sealed under the same dir and key
		// across restarts, so enrolment happens once per run.
		if suiteCred == "" {
			cred, hostID, err := enrollAttachCredential(url, "conformance-suite")
			if err != nil {
				return fmt.Errorf("enrol suite runner: %w", err)
			}
			suiteCred, suiteHost = cred, hostID
		}
		r, err := harness.StartRunner(url, suiteCred, suiteHost, specs)
		if err != nil {
			return fmt.Errorf("attach suite runner: %w", err)
		}
		suiteRunner = r
		return waitOfferings(url, len(offerings), timeout)
	}
	halt := func() {
		if suiteRunner != nil {
			suiteRunner.Close()
			suiteRunner = nil
		}
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

// waitOfferings blocks until the broker publishes want frozen offering
// tuples. Health only says the process is up; the offers do not exist
// until the runner has attached, matched and certified, and a scenario
// that runs before that races the freeze.
func waitOfferings(url string, want int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last string
	for time.Now().Before(deadline) {
		resp, err := http.Get(strings.TrimRight(url, "/") + "/registry/offerings")
		if err == nil {
			var doc struct {
				Capabilities []json.RawMessage `json:"capabilities"`
			}
			dec := json.NewDecoder(resp.Body)
			decErr := dec.Decode(&doc)
			_ = resp.Body.Close()
			if decErr == nil && len(doc.Capabilities) >= want {
				return nil
			}
			last = fmt.Sprintf("%d of %d offerings published", len(doc.Capabilities), want)
		} else {
			last = err.Error()
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("offerings not published within %s: %s", timeout, last)
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

// conformanceAdminToken is the admin bearer the generated host-config
// points at. It never leaves this process tree.
const conformanceAdminToken = "conformance-admin-token"

// enrollAttachCredential mints the credential the attach scenarios
// present (runner-attach §3.1.1). Auto mode can do this because it owns
// the broker; in URL mode the operator passes --attach-credential.
func enrollAttachCredential(brokerURL, host string) (credential, hostID string, err error) {
	body := strings.NewReader(fmt.Sprintf(`{"host_id":%q,"label":"livepeer-conformance"}`, host))
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(brokerURL, "/")+"/admin/v1/enroll", body)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+conformanceAdminToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("enroll returned %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var out struct {
		HostID     string `json:"host_id"`
		Credential struct {
			Token string `json:"token"`
		} `json:"credential"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", "", err
	}
	if out.Credential.Token == "" {
		return "", "", fmt.Errorf("enroll returned no token")
	}
	return out.Credential.Token, out.HostID, nil
}
