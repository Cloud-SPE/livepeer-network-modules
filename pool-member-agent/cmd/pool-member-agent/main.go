// Command pool-member-agent is the host-side agent shipped in the
// signup bundle. It attaches outbound to a broker, declares what this
// host runs (livepeer-network-protocol/protocols/runner-attach.md), and
// serves the work the broker dispatches back down the same connection.
//
// One bundle shape serves both deployments (plan 0043 decision 2): a
// pool member and an orchestrator's own hardware ("a pool of one") run
// the same binary with the same variables. What differs is only who
// minted the attach credential — the pool controller, or the broker's
// own POST /admin/v1/enroll.
//
// The agent never opens a listener, never holds a price, and never
// decides what is sold.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-member-agent/internal/attach"
	"github.com/gorilla/websocket"
	"github.com/quic-go/quic-go"
)

// version is stamped at build time; it rides the attach document as
// agent_version, which is audit-only.
var version = "dev"

// LocalIDHeader is the routing key the broker sets on every request it
// dispatches (runner-attach §7). The agent MUST route on it and not on
// the path: one host can serve the same capability_id under two models.
const LocalIDHeader = "Livepeer-Runner-Local-Id"

type config struct {
	BrokerURL      string
	BrokerQUICAddr string
	HostID         string
	Credential     string
	RunnersFile    string
	Runners        []attach.Runner
	RefreshEvery   time.Duration

	// Desired-state loop (plan 0044 §3.4). Empty ControllerURL means
	// this host is configured by hand: the agent attaches with whatever
	// runners it was told about and never polls. That is the standalone
	// mode, and it stays supported — a pool is one way to run a runner,
	// not the only way.
	ControllerURL   string
	EnrollmentID    string
	EnrollmentToken string
	ComposeFile     string
	ComposeBinary   string
	ComposeArgs     []string
	PollEvery       time.Duration
	PollTimeout     time.Duration
	// RotateEvery is how often the agent refreshes its own enrollment
	// credential. Default 24h — well inside any plausible lifetime,
	// because a host that waits for expiry has already stopped earning
	// by the time anyone can act on it.
	RotateEvery         time.Duration
	EnrollmentTokenFile string
}

// PoolManaged reports whether this host takes its runner set from a
// pool controller rather than from local configuration.
func (c config) PoolManaged() bool {
	return c.ControllerURL != "" && c.EnrollmentID != "" && c.EnrollmentToken != ""
}

type tunnelMessage struct {
	Type       string              `json:"type"`
	ID         string              `json:"id"`
	Body       json.RawMessage     `json:"body,omitempty"`
	Method     string              `json:"method,omitempty"`
	URL        string              `json:"url,omitempty"`
	Headers    map[string][]string `json:"headers,omitempty"`
	BodyBase64 string              `json:"body_base64,omitempty"`
	StatusCode int                 `json:"status_code,omitempty"`
	Error      string              `json:"error,omitempty"`
}

// registerResult is the broker's answer to a register (runner-attach §6).
type registerResult struct {
	ContractVersion string `json:"contract_version"`
	Document        string `json:"document"`
	HostID          string `json:"host_id"`
	Reasons         []struct {
		Code     string `json:"code"`
		Field    string `json:"field,omitempty"`
		Declared string `json:"declared,omitempty"`
		Expected string `json:"expected,omitempty"`
		Message  string `json:"message,omitempty"`
	} `json:"reasons"`
	Capabilities []struct {
		Index        int    `json:"index"`
		LocalID      string `json:"local_id"`
		CapabilityID string `json:"capability_id"`
		Status       string `json:"status"`
		Reasons      []struct {
			Code     string `json:"code"`
			Field    string `json:"field,omitempty"`
			Declared string `json:"declared,omitempty"`
			Expected string `json:"expected,omitempty"`
			Message  string `json:"message,omitempty"`
		} `json:"reasons,omitempty"`
	} `json:"capabilities"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, args []string) error {
	cfg, err := loadConfig(args)
	if err != nil {
		return err
	}
	if cfg.BrokerURL == "" && cfg.BrokerQUICAddr == "" {
		return errors.New("LIVEPEER_BROKER_URL or LIVEPEER_BROKER_QUIC_ADDR is required")
	}
	if cfg.Credential == "" {
		return errors.New("LIVEPEER_ATTACH_CREDENTIAL_FILE (or LIVEPEER_ATTACH_CREDENTIAL) is required")
	}
	if len(cfg.Runners) == 0 {
		log.Printf("warning: no runners declared — attaching with hardware only; " +
			"this host announces itself but serves nothing until runners are configured")
	}
	state := newRunnerState()
	state.set(cfg.Runners, "")
	if cfg.PoolManaged() {
		// The pool owns the runner set. Whatever was configured locally
		// is a starting point at most: the first reconcile replaces it.
		log.Printf("pool-managed: polling %s for enrollment %s every %s",
			cfg.ControllerURL, cfg.EnrollmentID, cfg.PollEvery)
		loopCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		go desiredLoop(loopCtx, cfg, state, nil)
	}

	err = tunnelLoop(ctx, cfg, state)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

func loadConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("pool-member-agent", flag.ExitOnError)
	refreshEvery := fs.Duration("refresh-every", envDuration("LIVEPEER_REFRESH_EVERY", time.Minute),
		"how often to rebuild the attach document and re-send it if it changed")
	showVersion := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)
	if *showVersion {
		fmt.Println(version)
		os.Exit(0)
	}
	cfg := config{
		BrokerURL:      strings.TrimRight(os.Getenv("LIVEPEER_BROKER_URL"), "/"),
		BrokerQUICAddr: strings.TrimSpace(os.Getenv("LIVEPEER_BROKER_QUIC_ADDR")),
		HostID:         strings.TrimSpace(os.Getenv("LIVEPEER_HOST_ID")),
		RunnersFile:    strings.TrimSpace(os.Getenv("LIVEPEER_RUNNERS_FILE")),

		ControllerURL:       strings.TrimRight(strings.TrimSpace(os.Getenv("POOL_CONTROLLER_URL")), "/"),
		EnrollmentID:        strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_ID")),
		EnrollmentToken:     enrollmentToken(),
		ComposeFile:         envOr("POOL_COMPOSE_FILE", "runners.compose.yaml"),
		ComposeBinary:       strings.TrimSpace(os.Getenv("POOL_COMPOSE_BINARY")),
		PollEvery:           envDuration("POOL_POLL_EVERY", 30*time.Second),
		PollTimeout:         envDuration("POOL_POLL_TIMEOUT", 30*time.Second),
		RotateEvery:         envDuration("POOL_ROTATE_EVERY", 24*time.Hour),
		EnrollmentTokenFile: strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_TOKEN_FILE")),
		RefreshEvery:        *refreshEvery,
	}
	if cfg.RefreshEvery <= 0 {
		cfg.RefreshEvery = time.Minute
	}
	cred, err := loadCredential()
	if err != nil {
		return cfg, err
	}
	cfg.Credential = cred
	runners, err := loadRunners(cfg.RunnersFile)
	if err != nil {
		return cfg, err
	}
	cfg.Runners = runners
	if cfg.HostID == "" {
		cfg.HostID = defaultHostID()
	}
	return cfg, nil
}

func loadCredential() (string, error) {
	if path := strings.TrimSpace(os.Getenv("LIVEPEER_ATTACH_CREDENTIAL_FILE")); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read attach credential: %w", err)
		}
		return strings.TrimSpace(string(raw)), nil
	}
	// The file is the documented form; the inline variable exists for
	// throwaway runs and is not what a bundle should use.
	return strings.TrimSpace(os.Getenv("LIVEPEER_ATTACH_CREDENTIAL")), nil
}

// loadRunners reads the runner declarations: a JSON file (what the pool
// controller generates and a standalone operator hand-writes), or the
// single-runner environment form for the simplest deployment.
func loadRunners(path string) ([]attach.Runner, error) {
	if path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read runners file: %w", err)
		}
		var runners []attach.Runner
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&runners); err != nil {
			return nil, fmt.Errorf("parse runners file %s: %w", path, err)
		}
		return runners, nil
	}
	// One runner from the environment. Its URL is the whole declaration:
	// what it serves is read from the runner itself at attach.
	url := strings.TrimRight(strings.TrimSpace(os.Getenv("LIVEPEER_RUNNER_URL")), "/")
	if url == "" {
		return nil, nil
	}
	return []attach.Runner{{
		LocalID: envOr("LIVEPEER_RUNNER_LOCAL_ID", "runner-0"),
		URL:     url,
	}}, nil
}

func defaultHostID() string {
	if h, err := os.Hostname(); err == nil && h != "" {
		return sanitizeID(h)
	}
	return "host-unknown"
}

// buildDocument assembles the current attach document: live hardware
// plus the declared runners. Hardware is re-read every time, so a GPU
// that appears or fails shows up on the next refresh.
func buildDocument(ctx context.Context, cfg config) (*attach.Document, error) {
	hw := collectHardware(ctx, cfg.HostID)
	// Every runner says what it is, or is named and left out. A missing
	// contract is the operator's signal — this log line IS the inventory
	// of runners that do not adhere — and it must not keep the rest of
	// the host from attaching. A host with nothing resolved attaches
	// hardware-only, which is visible on the broker as connected and
	// serving nothing, rather than not visible at all.
	resolved, errs := attach.Resolve(ctx, contractClient, cfg.Runners)
	for _, err := range errs {
		log.Printf("RUNNER HAS NO CONTRACT: %v", err)
	}
	return attach.Build(attach.Host{
		HostID:       cfg.HostID,
		AgentVersion: "pool-member-agent/" + version,
		Credential:   attach.Credential{Kind: "bearer", Token: cfg.Credential},
		Hardware:     hw,
	}, resolved)
}

// contractClient reads runner contracts. Short, because a runner that
// does not answer its own well-known path in seconds is not going to.
var contractClient = &http.Client{Timeout: 5 * time.Second}

func tunnelLoop(ctx context.Context, cfg config, state *runnerState) error {
	backoff := time.Second
	for {
		start := time.Now()
		if err := runTunnel(ctx, cfg, state); err != nil && !errors.Is(err, context.Canceled) {
			log.Printf("attach session ended: %v", err)
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A session that lasted signals a healthy config; reset so a
		// long-lived runner reconnects promptly after a broker restart.
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func runTunnel(ctx context.Context, cfg config, state *runnerState) error {
	// The runner set comes from the shared state, so a session started
	// after a placement change declares the new set rather than the one
	// this process booted with.
	if state != nil {
		runners, revision := state.get()
		cfg.Runners = runners
		if revision != "" {
			log.Printf("attaching with desired state %s (%d runner(s))", revision, len(runners))
		}
	}
	doc, err := buildDocument(ctx, cfg)
	if err != nil {
		// A document this agent knows is invalid is a configuration bug:
		// say so plainly rather than retrying into a broker rejection.
		return fmt.Errorf("build attach document: %w", err)
	}
	if cfg.BrokerQUICAddr != "" {
		return runQUICTunnel(ctx, cfg, state, doc)
	}
	return runWSTunnel(ctx, cfg, state, doc)
}

// logRegisterResult turns the broker's verdict into the operator's
// feedback loop. Every rejection names the field and both sides, so the
// log line is enough to fix the runner.
func logRegisterResult(res *registerResult) error {
	if res.Document == "rejected" {
		for _, r := range res.Reasons {
			log.Printf("ATTACH REJECTED: %s %s declared=%q expected=%q %s",
				r.Code, r.Field, r.Declared, r.Expected, r.Message)
		}
		return fmt.Errorf("broker rejected the attach document")
	}
	accepted := 0
	for _, c := range res.Capabilities {
		if c.Status == "accepted" {
			accepted++
			continue
		}
		for _, r := range c.Reasons {
			log.Printf("CAPABILITY REJECTED %s (%s): %s %s declared=%q expected=%q %s",
				c.LocalID, c.CapabilityID, r.Code, r.Field, r.Declared, r.Expected, r.Message)
		}
	}
	log.Printf("attached host=%s capabilities=%d/%d", res.HostID, accepted, len(res.Capabilities))
	return nil
}

func runWSTunnel(ctx context.Context, cfg config, state *runnerState, doc *attach.Document) error {
	sessionURL, err := workerSessionURL(cfg.BrokerURL)
	if err != nil {
		return err
	}
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, sessionURL, nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	var writeMu sync.Mutex
	send := func(msg tunnelMessage) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		return conn.WriteJSON(msg)
	}
	if err := sendRegister(send, doc); err != nil {
		return err
	}

	routes := attach.RouteTable(cfg.Runners)
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go refreshLoop(sessionCtx, cfg, state, doc, send)

	for {
		var msg tunnelMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		switch msg.Type {
		case "register_result":
			var res registerResult
			if err := json.Unmarshal(msg.Body, &res); err != nil {
				return fmt.Errorf("decode register_result: %w", err)
			}
			if err := logRegisterResult(&res); err != nil {
				return err
			}
		case "request":
			go func(m tunnelMessage) {
				resp := forwardTunnelRequest(sessionCtx, routes, m)
				if err := send(resp); err != nil {
					log.Printf("tunnel response write failed: %v", err)
				}
			}(msg)
		}
	}
}

func sendRegister(send func(tunnelMessage) error, doc *attach.Document) error {
	body, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	return send(tunnelMessage{Type: "register", ID: "register", Body: body})
}

// refreshLoop re-sends the document when it changes (runner-attach §2:
// each document fully replaces the previous one on that connection).
// Hardware appearing or disappearing is the common case.
func refreshLoop(ctx context.Context, cfg config, state *runnerState, current *attach.Document, send func(tunnelMessage) error) {
	last, err := json.Marshal(current)
	if err != nil {
		return
	}
	var wake <-chan struct{}
	if state != nil {
		wake = state.wake()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-wake:
			// A placement changed. Re-register now rather than at the
			// next tick: the pool may be withdrawing a runner, and the
			// broker has to stop dispatching before the container does.
		case <-time.After(cfg.RefreshEvery):
		}
		// Rebuild from the shared state, not from the config this
		// session started with — otherwise a pool-managed host would
		// re-send the runner set it booted with forever.
		sessionCfg := cfg
		if state != nil {
			runners, _ := state.get()
			sessionCfg.Runners = runners
		}
		next, err := buildDocument(ctx, sessionCfg)
		if err != nil {
			log.Printf("rebuild attach document: %v", err)
			continue
		}
		raw, err := json.Marshal(next)
		if err != nil || bytes.Equal(raw, last) {
			continue
		}
		if err := sendRegister(send, next); err != nil {
			log.Printf("re-send attach document: %v", err)
			return
		}
		last = raw
		log.Printf("attach document changed; re-sent")
	}
}

func runQUICTunnel(ctx context.Context, cfg config, state *runnerState, doc *attach.Document) error {
	conn, err := quic.DialAddr(ctx, cfg.BrokerQUICAddr, quicClientTLSConfig(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = conn.CloseWithError(0, "closed") }()
	if err := quicRegister(ctx, conn, doc); err != nil {
		return err
	}
	routes := attach.RouteTable(cfg.Runners)
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	// QUIC re-registers on the same terms as the websocket. It did not
	// before, which meant a QUIC-attached host could never announce a
	// change — including a drain, which the broker has to see before
	// the container stops.
	go refreshLoop(sessionCtx, cfg, state, doc, func(msg tunnelMessage) error {
		return quicSend(sessionCtx, conn, msg)
	})
	for {
		stream, err := conn.AcceptStream(ctx)
		if err != nil {
			return err
		}
		go handleQUICRequest(ctx, stream, routes)
	}
}

// quicSend writes one message on its own stream. QUIC has no single
// long-lived channel back to the broker the way the websocket does, so
// a re-register opens a stream, says its piece, and closes.
func quicSend(ctx context.Context, conn *quic.Conn, msg tunnelMessage) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	return json.NewEncoder(stream).Encode(msg)
}

// quicRegister opens a stream, sends the document, and reads the
// register_result the broker writes back on it.
func quicRegister(ctx context.Context, conn *quic.Conn, doc *attach.Document) error {
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(doc)
	if err != nil {
		_ = stream.Close()
		return err
	}
	if err := json.NewEncoder(stream).Encode(tunnelMessage{Type: "register", ID: "register", Body: body}); err != nil {
		_ = stream.Close()
		return err
	}
	var resp tunnelMessage
	if err := json.NewDecoder(stream).Decode(&resp); err != nil {
		return fmt.Errorf("read register_result: %w", err)
	}
	if resp.Error != "" {
		return fmt.Errorf("quic register failed: %s", resp.Error)
	}
	var res registerResult
	if err := json.Unmarshal(resp.Body, &res); err != nil {
		return fmt.Errorf("decode register_result: %w", err)
	}
	return logRegisterResult(&res)
}

func quicClientTLSConfig() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"livepeer-pool-worker/1"}}
}

func writeQUICFrameHeader(w io.Writer, msg tunnelMessage) error {
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if len(raw) > 16*1024*1024 {
		return fmt.Errorf("quic frame header too large")
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(raw)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err = w.Write(raw)
	return err
}

func readQUICFrameHeader(r io.Reader) (tunnelMessage, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return tunnelMessage{}, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 16*1024*1024 {
		return tunnelMessage{}, fmt.Errorf("invalid quic frame header length %d", n)
	}
	raw := make([]byte, n)
	if _, err := io.ReadFull(r, raw); err != nil {
		return tunnelMessage{}, err
	}
	var msg tunnelMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return tunnelMessage{}, err
	}
	return msg, nil
}

func handleQUICRequest(ctx context.Context, stream *quic.Stream, routes map[string]string) {
	defer func() { _ = stream.Close() }()
	msg, err := readQUICFrameHeader(stream)
	if err != nil {
		_ = writeQUICFrameHeader(stream, tunnelMessage{Type: "response", Error: err.Error()})
		return
	}
	if msg.Type != "request" {
		_ = writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: "expected request"})
		return
	}
	if err := forwardQUICRequest(ctx, stream, routes, msg); err != nil {
		log.Printf("worker quic response write failed: %v", err)
	}
}

func forwardQUICRequest(ctx context.Context, stream *quic.Stream, routes map[string]string, msg tunnelMessage) error {
	base, err := routeFor(routes, msg.Headers)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	target, err := joinBackendURL(base, msg.URL)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	req, err := http.NewRequestWithContext(ctx, defaultMethod(msg.Method), target, stream)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	req.Header = runnerHeaders(msg.Headers)
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		return writeQUICFrameHeader(stream, tunnelMessage{Type: "response", ID: msg.ID, Error: err.Error()})
	}
	defer func() { _ = httpResp.Body.Close() }()
	if err := writeQUICFrameHeader(stream, tunnelMessage{
		Type:       "response",
		ID:         msg.ID,
		StatusCode: httpResp.StatusCode,
		Headers:    map[string][]string(httpResp.Header),
	}); err != nil {
		return err
	}
	_, err = io.Copy(stream, httpResp.Body)
	return err
}

func forwardTunnelRequest(ctx context.Context, routes map[string]string, msg tunnelMessage) tunnelMessage {
	resp := tunnelMessage{Type: "response", ID: msg.ID}
	base, err := routeFor(routes, msg.Headers)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	target, err := joinBackendURL(base, msg.URL)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	body, err := base64Decode(msg.BodyBase64)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	req, err := http.NewRequestWithContext(ctx, defaultMethod(msg.Method), target, bytes.NewReader(body))
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	req.Header = runnerHeaders(msg.Headers)
	httpResp, err := http.DefaultClient.Do(req)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	defer func() { _ = httpResp.Body.Close() }()
	respBody, err := io.ReadAll(httpResp.Body)
	if err != nil {
		resp.Error = err.Error()
		return resp
	}
	resp.StatusCode = httpResp.StatusCode
	resp.Headers = map[string][]string(httpResp.Header)
	resp.BodyBase64 = base64Encode(respBody)
	return resp
}

// routeFor selects the runner by the broker's routing header
// (runner-attach §7). Routing on the path instead would send work for
// two models behind one capability id to whichever container was
// listed first.
func routeFor(routes map[string]string, headers map[string][]string) (string, error) {
	localID := headerValue(headers, LocalIDHeader)
	if base := routes[localID]; base != "" {
		return base, nil
	}
	if localID == "" && len(routes) == 1 {
		for _, only := range routes {
			return only, nil
		}
	}
	if localID == "" {
		return "", fmt.Errorf("no %s header and this host serves %d runners", LocalIDHeader, len(routes))
	}
	return "", fmt.Errorf("no runner with local_id %q", localID)
}

// runnerHeaders strips the broker's routing header before forwarding:
// it is tunnel plumbing, not something a runner should see.
func runnerHeaders(in map[string][]string) http.Header {
	out := http.Header(in).Clone()
	if out == nil {
		out = http.Header{}
	}
	out.Del(LocalIDHeader)
	out.Del("X-Livepeer-Worker-Backend-Id")
	return out
}

func collectNVIDIAGPUs(ctx context.Context) ([]attach.Hardware, error) {
	cmd := exec.CommandContext(ctx, "nvidia-smi",
		"--query-gpu=uuid,name,memory.total,driver_version", "--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi query failed: %w", err)
	}
	return parseNVIDIASMI(out)
}

func workerSessionURL(rawBrokerURL string) (string, error) {
	if rawBrokerURL == "" {
		return "", errors.New("LIVEPEER_BROKER_URL is required for the websocket transport")
	}
	u, err := url.Parse(rawBrokerURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported broker url scheme %q", u.Scheme)
	}
	// No backend_ids: their absence is what selects the attach path.
	u.Path = strings.TrimRight(u.Path, "/") + "/internal/v1/worker/session"
	return u.String(), nil
}

func joinBackendURL(baseURL, tunnelURL string) (string, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil {
		return "", err
	}
	tunnel, err := url.Parse(tunnelURL)
	if err != nil {
		return "", err
	}
	base.Path = strings.TrimRight(base.Path, "/") + "/" + strings.TrimLeft(tunnel.Path, "/")
	base.RawQuery = tunnel.RawQuery
	return base.String(), nil
}

func headerValue(headers map[string][]string, key string) string {
	for gotKey, values := range headers {
		if strings.EqualFold(gotKey, key) && len(values) > 0 {
			return strings.TrimSpace(values[0])
		}
	}
	return ""
}

func defaultMethod(method string) string {
	if strings.TrimSpace(method) == "" {
		return http.MethodPost
	}
	return method
}

func base64Encode(body []byte) string {
	return base64.StdEncoding.EncodeToString(body)
}

func base64Decode(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	return base64.StdEncoding.DecodeString(raw)
}

func parseNVIDIASMI(out []byte) ([]attach.Hardware, error) {
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	units := make([]attach.Hardware, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) < 4 {
			return nil, fmt.Errorf("unexpected nvidia-smi row %q", line)
		}
		uuid := strings.TrimSpace(parts[0])
		name := strings.TrimSpace(parts[1])
		memoryMiB, err := strconv.ParseUint(strings.TrimSpace(parts[2]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse gpu memory for %s: %w", uuid, err)
		}
		driver := strings.TrimSpace(parts[3])
		units = append(units, attach.Hardware{
			GPUUUID:   uuid,
			GPUModel:  name,
			VRAMBytes: memoryMiB * 1024 * 1024,
			Driver:    driver,
			Facts:     map[string]string{"source": "nvidia-smi"},
		})
	}
	return units, nil
}

func sanitizeID(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	replacer := strings.NewReplacer(" ", "-", "_", "-", "/", "-", ":", "-")
	return replacer.Replace(raw)
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

// enrollmentToken reads the token the bundle shipped. A file is
// preferred over the environment: the token is a credential, and an
// environment variable is visible to every process on the host.
func enrollmentToken() string {
	if path := strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_TOKEN_FILE")); path != "" {
		if raw, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(raw))
		}
	}
	return strings.TrimSpace(os.Getenv("POOL_ENROLLMENT_TOKEN"))
}
