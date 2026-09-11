package e2e

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// pool is one running deployment: a broker and a controller, wired to
// each other, with a scratch directory that dies with the test.
type pool struct {
	t          *testing.T
	dir        string
	brokerURL  string
	controlURL string
	adminToken string
	procs      []*exec.Cmd
}

const (
	brokerAdminToken     = "e2e-broker-admin-token"
	controllerAdminToken = "e2e-controller-admin-token"
	// orchAddress is the identity both sides advertise. It is a real
	// 40-hex address because the broker validates the shape.
	orchAddress = "0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"
)

// startPool boots both services and returns when each answers.
//
// The binaries are run with `go run` rather than imported, because the
// packages that matter are internal and — more to the point — a test
// that imported them would prove the types agree, which is never what
// broke.
func startPool(t *testing.T, catalogDir string) *pool {
	t.Helper()
	dir := t.TempDir()
	p := &pool{t: t, dir: dir, adminToken: controllerAdminToken}
	t.Cleanup(p.stop)

	brokerPort := freePort(t)
	brokerMetrics := freePort(t)
	controlPort := freePort(t)
	controlMetrics := freePort(t)
	p.brokerURL = fmt.Sprintf("http://127.0.0.1:%d", brokerPort)
	p.controlURL = fmt.Sprintf("http://127.0.0.1:%d", controlPort)

	sealKey := make([]byte, 32)
	if _, err := rand.New(rand.NewSource(1)).Read(sealKey); err != nil {
		t.Fatalf("seal key: %v", err)
	}
	sealPath := filepath.Join(dir, "seal.key")
	write(t, sealPath, hex.EncodeToString(sealKey))

	// The broker takes its offers from the controller, which is the
	// whole point: a file-configured broker would prove nothing about
	// whether the push works.
	brokerCfg := filepath.Join(dir, "broker.yaml")
	write(t, brokerCfg, fmt.Sprintf(`identity:
  orch_eth_address: %q
external_base_url: "http://127.0.0.1:%d"
listen:
  paid: "127.0.0.1:%d"
  metrics: "127.0.0.1:%d"
payment_daemon:
  mock: true
  mock_state_path: %q
session_store:
  path: %q
  sealing_key_file: %q
admin_auth:
  method: bearer
  secret_ref: env://E2E_BROKER_ADMIN_TOKEN
credential_store:
  path: %q
  sealing_key_file: %q
offers_source: admin
offers_state_path: %q
`, orchAddress, brokerPort, brokerPort, brokerMetrics,
		filepath.Join(dir, "payment.json"),
		filepath.Join(dir, "sessions.db"), sealPath,
		filepath.Join(dir, "credentials.db"), sealPath,
		filepath.Join(dir, "offers.db")))

	controlCfg := filepath.Join(dir, "controller.yaml")
	write(t, controlCfg, fmt.Sprintf(`identity:
  orch_eth_address: %q
  label: "e2e-pool"
listen:
  paid: "127.0.0.1:%d"
  metrics: "127.0.0.1:%d"
template_catalog_dir: %q
admin_auth:
  bearer_token_ref: env://E2E_CONTROLLER_ADMIN_TOKEN
bootstrap:
  brokers:
    - name: e2e-broker
      admin_url: %q
      auth:
        method: bearer
        secret_ref: env://E2E_BROKER_ADMIN_TOKEN
      timeout_ms: 5000
claims:
  sweep_interval_minutes: 60
`, orchAddress, controlPort, controlMetrics, catalogDir, p.brokerURL))

	env := append(os.Environ(),
		"E2E_BROKER_ADMIN_TOKEN="+brokerAdminToken,
		"E2E_CONTROLLER_ADMIN_TOKEN="+controllerAdminToken,
	)
	p.run(env, "../capability-broker", "./cmd/livepeer-capability-broker", "--config", brokerCfg)
	p.waitHealthy(p.brokerURL+"/healthz", "broker")
	p.run(env, "../pool-controller", "./cmd/livepeer-pool-controller", "serve",
		"--config", controlCfg, "--data-dir", filepath.Join(dir, "controller-data"))
	p.waitHealthy(p.controlURL+"/public/v1/summary", "controller")
	return p
}

func (p *pool) run(env []string, dir string, args ...string) {
	p.t.Helper()
	cmd := exec.Command("go", append([]string{"run"}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	// Own process group, so killing reaches the binary `go run` spawns
	// rather than only go run itself — otherwise a failed test leaves a
	// broker holding its port.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		p.t.Fatalf("start %v: %v", args, err)
	}
	p.procs = append(p.procs, cmd)
}

func (p *pool) stop() {
	for _, cmd := range p.procs {
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			_, _ = cmd.Process.Wait()
		}
	}
}

func (p *pool) waitHealthy(url, name string) {
	p.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	p.t.Fatalf("%s never became healthy at %s", name, url)
}

// do issues an authenticated request against either service.
func (p *pool) do(method, url, token, body string) (int, []byte) {
	p.t.Helper()
	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, rdr)
	if err != nil {
		p.t.Fatalf("request: %v", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatalf("%s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

func (p *pool) controller(method, path, body string) (int, []byte) {
	return p.do(method, p.controlURL+path, controllerAdminToken, body)
}

func (p *pool) broker(method, path, body string) (int, []byte) {
	return p.do(method, p.brokerURL+path, brokerAdminToken, body)
}

// decode unmarshals a response or fails with what actually came back,
// which is the difference between a useful failure and a nil map.
func decode(t *testing.T, raw []byte, out any) {
	t.Helper()
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode: %v\nbody: %s", err, string(raw))
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// eventually polls until check passes, so a test asserts an outcome
// rather than a sleep. Both services do real work on timers.
func eventually(t *testing.T, what string, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = check(); last == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("%s did not happen within %s: %v", what, timeout, last)
}
