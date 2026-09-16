package e2e

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
	protocolv1 "github.com/Cloud-SPE/livepeer-network-modules/proto-contracts/livepeer/protocol/v1"
	"google.golang.org/grpc"
)

type regionalClock struct {
	protocolv1.UnimplementedProtocolDaemonServer
}

func (*regionalClock) GetRoundStatus(context.Context, *protocolv1.Empty) (*protocolv1.RoundStatus, error) {
	return &protocolv1.RoundStatus{LastRound: 128, CurrentRoundInitialized: true}, nil
}

func fixtureJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(raw))
}

// sourceProcess is a real broker plus a real receiver service. Synthetic chain
// inclusions and already-completed metering are provisioned by test-only code;
// collection, durable delivery and round/window accounting use production code.
type sourceProcess struct {
	Source                 revenue.Source
	receiver, broker       *exec.Cmd
	role                   string
	receiverConfig, socket string
}
type accountingRegion struct {
	controller                              *exec.Cmd
	p                                       *pool
	id, data, config, reconcile, adminToken string
	sources                                 []sourceProcess
}

func runOfflineFixture(t *testing.T, dir, pkg, testName, env, path string) {
	t.Helper()
	cmd := exec.Command("go", "test", pkg, "-run", "^"+testName+"$", "-count=1")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env+"="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v %s", testName, err, out)
	}
}
func (p *pool) runTestFixture(dir, pkg, testName, env, path string) *exec.Cmd {
	p.t.Helper()
	cmd := exec.Command("go", "test", pkg, "-run", "^"+testName+"$", "-count=1", "-timeout", "15m")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env+"="+path)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		p.t.Fatal(err)
	}
	p.procs = append(p.procs, cmd)
	return cmd
}

func sourceConfigs(sources []revenue.Source) []map[string]any {
	out := []map[string]any{}
	for _, s := range sources {
		out = append(out, map[string]any{"pool_id": s.PoolID, "source_id": s.SourceID, "broker_id": s.BrokerID, "chain_id": s.ChainID, "payee": s.Payee, "url": s.URL, "token_file": s.TokenFile, "ca_file": s.CAFile})
	}
	return out
}
func socketDirectory(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "regional-sockets-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func startAccountingRegion(t *testing.T, name, wallet, other string, roles []string, sourceOffset int, clockSocket string) *accountingRegion {
	t.Helper()
	p := &pool{t: t, dir: t.TempDir()}
	t.Cleanup(p.stop)
	r := &accountingRegion{p: p, data: filepath.Join(p.dir, "controller-data"), config: filepath.Join(p.dir, "controller.yaml"), reconcile: filepath.Join(p.dir, "reconciler.yaml")}
	cmd := exec.Command("go", "run", "./cmd/livepeer-pool-controller", "init-identity", "--data-dir", r.data)
	cmd.Dir = "../pool-controller"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("identity: %v %s", err, out)
	}
	var identity struct {
		PoolID string `json:"pool_id"`
	}
	decode(t, out, &identity)
	r.id = identity.PoolID
	controlPort := freePort(t)
	p.controlURL = fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	controlProxy := tlsProcessProxy(t, p.controlURL)
	ca := filepath.Join(p.dir, "controller-ca.pem")
	write(t, ca, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: controlProxy.Certificate().Raw})))
	var controllerCredentials []serviceauth.Credential
	credential := func(role, source string) string {
		token, hash, err := serviceauth.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.dir, fmt.Sprintf("controller-token-%d", len(controllerCredentials)))
		write(t, path, token)
		controllerCredentials = append(controllerCredentials, serviceauth.Credential{ID: filepath.Base(path), PoolID: r.id, SourceID: source, Roles: []string{role}, Resources: []string{"controller"}, TokenSHA256: hash, ExpiresAt: time.Now().Add(time.Hour)})
		return path
	}
	adminFile := credential("pool-admin", "")
	adminRaw, _ := os.ReadFile(adminFile)
	r.adminToken = string(adminRaw)
	reconcileToken := credential("reconciler", "")
	var sources []revenue.Source
	type startSource struct {
		cfg, work, receiver string
		env                 string
	}
	var pending []startSource
	for i, role := range roles {
		dir := filepath.Join(p.dir, role)
		if err = os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		brokerPort := freePort(t)
		brokerURL := fmt.Sprintf("http://127.0.0.1:%d", brokerPort)
		proxy := tlsProcessProxy(t, brokerURL)
		source := revenue.Source{PoolID: r.id, SourceID: fmt.Sprintf("0x%064x", sourceOffset+i+1), BrokerID: role, ChainID: 42161, Payee: orchAddress, URL: proxy.URL, CAFile: filepath.Join(dir, "ca.pem"), TokenFile: filepath.Join(dir, "reader-token")}
		write(t, source.CAFile, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw})))
		reader, readerHash, err := serviceauth.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		write(t, source.TokenFile, reader)
		brokerAuth := filepath.Join(dir, "service-auth.json")
		fixtureJSON(t, brokerAuth, serviceauth.File{Credentials: []serviceauth.Credential{{ID: "reader", PoolID: r.id, Roles: []string{"revenue-reader"}, Resources: []string{role}, TokenSHA256: readerHash, ExpiresAt: time.Now().Add(time.Hour)}}})
		brokerToken := credential("broker", source.SourceID)
		socket := filepath.Join(socketDirectory(t), "receiver.sock")
		receiverCfg := filepath.Join(dir, "receiver.json")
		workCfg := filepath.Join(dir, "work.json")
		workPath := filepath.Join(dir, "work.db")
		count, value, face, member := 601, int64(1), int64(10), wallet
		if name == "us" {
			face = 20
		}
		if role == "audio-broker" {
			count, value, face = 3, 7, 100
		}
		if role == "llm-broker" {
			count, value, face, member = 2, 100, 1000, other
		}
		fixtureJSON(t, receiverCfg, map[string]any{"Store": filepath.Join(dir, "receiver.db"), "Socket": socket, "Source": source.SourceID, "Payee": orchAddress, "Count": count, "FaceValue": face, "Round": 100, "Through": 127, "ZeroRoundRevenue": 7})
		fixtureJSON(t, workCfg, map[string]any{"Store": workPath, "PoolID": r.id, "Source": source.SourceID, "Broker": role, "Wallet": member, "Terms": name + "-v1", "Count": count, "Value": value, "Round": 100})
		seal := filepath.Join(dir, "seal.key")
		write(t, seal, strings.Repeat("a", 64))
		cfg := filepath.Join(dir, "broker.json")
		fixtureJSON(t, cfg, map[string]any{
			"pool_id": r.id, "service_resource": role, "service_auth_file": brokerAuth, "accounting_store_path": workPath,
			"identity": map[string]any{"orch_eth_address": orchAddress}, "external_base_url": brokerURL,
			"listen":         map[string]any{"paid": fmt.Sprintf("127.0.0.1:%d", brokerPort), "metrics": fmt.Sprintf("127.0.0.1:%d", freePort(t))},
			"payment_daemon": map[string]any{"socket": socket}, "session_store": map[string]any{"path": filepath.Join(dir, "sessions.db"), "sealing_key_file": seal},
			"credential_store": map[string]any{"path": filepath.Join(dir, "credentials.db"), "sealing_key_file": seal}, "offers_source": "admin", "offers_state_path": filepath.Join(dir, "offers.db"),
			"receipt_sink": map[string]any{"url": controlProxy.URL + "/admin/v1/work-receipts", "auth": map[string]any{"method": "scoped", "pool_id": r.id, "token_file": brokerToken, "ca_file": ca}},
		})
		r.sources = append(r.sources, sourceProcess{Source: source, role: role, receiverConfig: receiverCfg, socket: socket})
		sources = append(sources, source)
		pending = append(pending, startSource{cfg: cfg, work: workCfg, receiver: receiverCfg})
	}
	fixture := filepath.Join(p.dir, "provision.json")
	fixtureJSON(t, fixture, map[string]any{"DataDir": r.data, "Version": name + "-v1", "Output": filepath.Join(p.dir, "identity.json"), "Wallet": wallet, "Wallets": []string{other}, "Sources": sources})
	runOfflineFixture(t, "../pool-controller", "./internal/repo", "TestRegionalE2EProvisionTerms", "REGIONAL_E2E_PROVISION", fixture)
	auth := filepath.Join(p.dir, "controller-auth.json")
	fixtureJSON(t, auth, serviceauth.File{Credentials: controllerCredentials})
	fixtureJSON(t, r.config, map[string]any{"identity": map[string]any{"orch_eth_address": orchAddress, "label": name}, "listen": map[string]any{"paid": fmt.Sprintf("127.0.0.1:%d", controlPort), "metrics": fmt.Sprintf("127.0.0.1:%d", freePort(t))}, "service_auth_file": auth, "revenue_sources": sourceConfigs(sources)})
	p.run(os.Environ(), "../pool-controller", "./cmd/livepeer-pool-controller", "serve", "--config", r.config, "--data-dir", r.data)
	r.controller = p.procs[len(p.procs)-1]
	p.waitHealthy(p.controlURL+"/public/v1/summary", "accounting controller")
	for i, item := range pending {
		runOfflineFixture(t, "../capability-broker", "./internal/workledger", "TestRegionalE2EProvisionWork", "REGIONAL_E2E_WORK", item.work)
		r.sources[i].receiver = p.runTestFixture("../payment-daemon", "./internal/service/receiver", "TestRegionalE2EReceiver", "REGIONAL_E2E_RECEIVER", item.receiver)
		var receiver struct{ Socket string }
		raw, _ := os.ReadFile(item.receiver)
		decode(t, raw, &receiver)
		eventually(t, "receiver socket", 60*time.Second, func() error { _, err := os.Stat(receiver.Socket); return err })
		p.run(os.Environ(), "../capability-broker", "./cmd/livepeer-capability-broker", "--config", item.cfg)
		r.sources[i].broker = p.procs[len(p.procs)-1]
	}
	fixtureJSON(t, r.reconcile, map[string]any{"pool_controller": map[string]any{"pool_id": r.id, "url": controlProxy.URL, "token_file": reconcileToken, "ca_file": ca, "timeout_ms": 15000}, "revenue_sources": sourceConfigs(sources), "round_source": map[string]any{"protocol_daemon_socket": clockSocket}, "reconcile": map[string]any{"state_path": filepath.Join(p.dir, "reconciler.db")}})
	return r
}

func (r *accountingRegion) admin(method, path, body string) (int, []byte) {
	req, err := http.NewRequest(method, r.p.controlURL+path, strings.NewReader(body))
	if err != nil {
		r.p.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+r.adminToken)
	req.Header.Set(serviceauth.PoolHeader, r.id)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		r.p.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		r.p.t.Fatal(err)
	}
	return resp.StatusCode, raw
}
func (r *accountingRegion) closeRound(round int) ([]byte, error) {
	cmd := exec.Command("go", "run", "./cmd/livepeer-pool-reconciler", "close-round", "--config", r.reconcile, "--round-id", fmt.Sprint(round))
	cmd.Dir = "../pool-reconciler"
	return cmd.CombinedOutput()
}

func TestRegionalRealRevenueCollectionAndIndependentWindows(t *testing.T) {
	if testing.Short() {
		t.Skip("real component process accounting acceptance")
	}
	clockPath := filepath.Join(socketDirectory(t), "clock.sock")
	listener, err := net.Listen("unix", clockPath)
	if err != nil {
		t.Fatal(err)
	}
	clock := grpc.NewServer()
	protocolv1.RegisterProtocolDaemonServer(clock, &regionalClock{})
	go clock.Serve(listener)
	t.Cleanup(clock.Stop)
	wallet, other := "0x"+strings.Repeat("1", 40), "0x"+strings.Repeat("2", 40)
	eu := startAccountingRegion(t, "eu", wallet, other, []string{"eu-transcode-broker"}, 10, clockPath)
	us := startAccountingRegion(t, "us", wallet, other, []string{"us-transcode-broker", "audio-broker", "llm-broker"}, 20, clockPath)
	for _, r := range []*accountingRegion{eu, us} {
		expected := 601
		if r == us {
			expected = 606
		}
		eventually(t, "all durable work receipts delivered", 60*time.Second, func() error {
			status, raw := r.admin("GET", "/admin/v1/work-receipts?round_id=100&status=final&pagination=true&limit=500", "")
			if status != 200 {
				return fmt.Errorf("%d %s", status, raw)
			}
			var page struct {
				Total      int    `json:"total"`
				NextCursor string `json:"next_cursor"`
			}
			decode(t, raw, &page)
			if page.NextCursor == "" {
				return fmt.Errorf("expected %d receipts beyond first page: %s", expected, raw)
			}
			return nil
		})
	}
	// A unavailable US source must hold US, while EU can close its whole window.
	stopRegionalProcess(t, us.sources[2].receiver)
	if out, err := us.closeRound(100); err == nil {
		t.Fatalf("US accepted unavailable source: %s", out)
	}
	for round := 100; round < 114; round++ {
		if out, err := eu.closeRound(round); err != nil {
			t.Fatalf("EU close %d: %v %s", round, err, out)
		}
	}
	status, raw := eu.admin("POST", "/admin/v1/settlement-windows/close", `{"start_round_id":"100"}`)
	if status != 200 {
		t.Fatalf("EU close window %d %s", status, raw)
	}
	euBatch := checkRegionalWindow(t, raw, eu.id, "6010", "601", "601", "0", map[string]string{wallet: "5409"})
	// Restart the exact US receiver store and retry collection; no source is dropped.
	lost := &us.sources[2]
	if err := os.Remove(lost.socket); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	lost.receiver = us.p.runTestFixture("../payment-daemon", "./internal/service/receiver", "TestRegionalE2EReceiver", "REGIONAL_E2E_RECEIVER", lost.receiverConfig)
	eventually(t, "US receiver reporting recovery", 30*time.Second, func() error { _, err := revenue.Collect(context.Background(), lost.Source, 100); return err })
	for round := 100; round < 114; round++ {
		if out, err := us.closeRound(round); err != nil {
			t.Fatalf("US close %d: %v %s", round, err, out)
		}
	}
	status, raw = us.admin("POST", "/admin/v1/settlement-windows/close", `{"start_round_id":"100"}`)
	if status != 200 {
		t.Fatalf("US window %d %s", status, raw)
	}
	usBatch := checkRegionalWindow(t, raw, us.id, "14320", "822", "1432", "1", map[string]string{wallet: "9752", other: "3135"})
	if euBatch == usBatch {
		t.Fatal("regional batch identity collision")
	}
	// Approval and replay must preserve each pool's immutable obligations.
	for _, item := range []struct {
		r     *accountingRegion
		batch string
	}{{eu, euBatch}, {us, usBatch}} {
		status, raw = item.r.admin("POST", "/admin/v1/payout-batches/"+item.batch+"/approve", "")
		if status != 200 {
			t.Fatalf("approve %d %s", status, raw)
		}
		status, raw = item.r.admin("POST", "/admin/v1/payout-batches/"+item.batch+"/approve", "")
		if status != 200 {
			t.Fatalf("approve replay %d %s", status, raw)
		}
	}
	_, usBefore := us.admin("GET", "/admin/v1/payout-intents", "")
	status, raw = eu.admin("POST", "/admin/v1/payout-intents/claim", `{"executor_id":"eu-executor","limit":1,"lease_ttl_seconds":60}`)
	if status != 200 {
		t.Fatalf("claim %d %s", status, raw)
	}
	var claim struct {
		LeaseID string `json:"lease_id"`
		Intents []struct {
			ID string `json:"id"`
		}
	}
	decode(t, raw, &claim)
	if claim.LeaseID == "" || len(claim.Intents) != 1 {
		t.Fatal(string(raw))
	}
	body, _ := json.Marshal(map[string]any{"ids": []string{claim.Intents[0].ID}, "lease_id": claim.LeaseID, "status": "failed", "failure_reason": "synthetic pre-submit failure"})
	status, raw = eu.admin("POST", "/admin/v1/payout-intents/status", string(body))
	if status != 200 {
		t.Fatalf("failure %d %s", status, raw)
	}
	body, _ = json.Marshal(map[string]any{"ids": []string{claim.Intents[0].ID}})
	status, raw = eu.admin("POST", "/admin/v1/payout-intents/requeue", string(body))
	if status != 200 || !strings.Contains(string(raw), `"retry_count":1`) {
		t.Fatalf("retry %d %s", status, raw)
	}
	_, usAfter := us.admin("GET", "/admin/v1/payout-intents", "")
	if string(usBefore) != string(usAfter) {
		t.Fatal("EU retry changed US obligations")
	}
	// Reclosing cannot recalculate an approved window or reset its retry state.
	status, raw = eu.admin("POST", "/admin/v1/settlement-windows/close", `{"start_round_id":"100"}`)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	checkRegionalWindow(t, raw, eu.id, "6010", "601", "601", "0", map[string]string{wallet: "5409"})
	// Normal controller restart preserves the approved batch and its retry record.
	_, beforeRestart := eu.admin("GET", "/admin/v1/payout-intents", "")
	stopRegionalProcess(t, eu.controller)
	eu.p.run(os.Environ(), "../pool-controller", "./cmd/livepeer-pool-controller", "serve", "--config", eu.config, "--data-dir", eu.data)
	eu.controller = eu.p.procs[len(eu.p.procs)-1]
	eu.p.waitHealthy(eu.p.controlURL+"/public/v1/summary", "restarted EU controller")
	_, afterRestart := eu.admin("GET", "/admin/v1/payout-intents", "")
	if string(beforeRestart) != string(afterRestart) {
		t.Fatal("controller restart changed payout obligations")
	}
	if out, err := eu.closeRound(100); err != nil {
		t.Fatalf("reconciler durable replay: %v %s", err, out)
	}
	// A later complete window has real revenue but no work at all.
	for round := 114; round < 128; round++ {
		if out, err := eu.closeRound(round); err != nil {
			t.Fatalf("zero-work round %d: %v %s", round, err, out)
		}
	}
	status, raw = eu.admin("POST", "/admin/v1/settlement-windows/close", `{"start_round_id":"114"}`)
	if status != 200 {
		t.Fatal(status, string(raw))
	}
	var zero struct {
		Window struct {
			Allocation struct{ Revenue, Operator string } `json:"unused"`
			Regional   struct {
				Revenue    string `json:"revenue_wei"`
				Operator   string `json:"zero_work_operator_wei"`
				Commission string `json:"commission_wei"`
				Members    string `json:"member_payout_wei"`
			} `json:"regional_allocation"`
		}
		Batch struct {
			Total string `json:"total_amount_wei"`
		}
	}
	decode(t, raw, &zero)
	if zero.Window.Regional.Revenue != "7" || zero.Window.Regional.Operator != "7" || zero.Window.Regional.Commission != "0" || zero.Window.Regional.Members != "0" || zero.Batch.Total != "0" {
		t.Fatalf("zero-work allocation: %s", raw)
	}
}

func checkRegionalWindow(t *testing.T, raw []byte, poolID, revenue, weight, commission, dust string, members map[string]string) string {
	t.Helper()
	var result struct {
		Window struct {
			PoolID   string `json:"pool_id"`
			Length   uint64 `json:"length_rounds"`
			Regional struct {
				Revenue    string `json:"revenue_wei"`
				Weight     string `json:"billed_weight_wei"`
				Commission string `json:"commission_wei"`
				Dust       string `json:"rounding_residual_wei"`
			} `json:"regional_allocation"`
		}
		Batch struct {
			ID     string
			PoolID string `json:"pool_id"`
			Items  []struct {
				Wallet string `json:"member_eth_address"`
				Amount string `json:"amount_wei"`
			} `json:"line_items"`
		}
	}
	decode(t, raw, &result)
	a := result.Window.Regional
	if result.Window.PoolID != poolID || result.Batch.PoolID != poolID || result.Window.Length != 14 || a.Revenue != revenue || a.Weight != weight || a.Commission != commission || a.Dust != dust || len(result.Batch.Items) != len(members) {
		t.Fatalf("regional window mismatch: %s", raw)
	}
	for _, item := range result.Batch.Items {
		if members[item.Wallet] != item.Amount {
			t.Fatalf("member allocation mismatch: %s", raw)
		}
	}
	return result.Batch.ID
}
