package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/memberauth"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/ownership"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/revenue"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-commons/serviceauth"
)

func transferDocker(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("docker %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestRegionalTransferWithActualAgentContainerStop(t *testing.T) {
	if os.Getenv("REGIONAL_DOCKER_ACCEPTANCE") != "1" {
		t.Skip("requires explicitly enabled local Docker socket acceptance")
	}
	p := &pool{t: t, dir: t.TempDir()}
	t.Cleanup(p.stop)
	data := filepath.Join(p.dir, "controller")
	cmd := exec.Command("go", "run", "./cmd/livepeer-pool-controller", "init-identity", "--data-dir", data)
	cmd.Dir = "../pool-controller"
	raw, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("identity: %v %s", err, raw)
	}
	var identity struct {
		PoolID string `json:"pool_id"`
	}
	decode(t, raw, &identity)
	sourcePool, targetPool := identity.PoolID, "pool_transfer_destination"
	wallet := "0x" + strings.Repeat("1", 40)
	hostID := "transfer-" + strings.TrimPrefix(sourcePool, "pool_")
	agentToken, attachToken := "fixture-controller-agent-secret", "fixture-broker-attach-secret"

	// A real ownership process serves its own HTTPS listener and persistent DB.
	certSource := httptest.NewTLSServer(http.NotFoundHandler())
	cert := certSource.TLS.Certificates[0]
	certSource.Close()
	ca := filepath.Join(p.dir, "ca.pem")
	key := filepath.Join(p.dir, "tls-key.pem")
	write(t, ca, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})))
	private, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	write(t, key, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})))
	ownerURL := fmt.Sprintf("https://127.0.0.1:%d", freePort(t))
	ownerAuth := filepath.Join(p.dir, "ownership-auth.json")
	var ownerCreds []serviceauth.Credential
	ownerConfig := func(poolID, role string) ownership.Config {
		token, digest, err := serviceauth.GenerateToken()
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.dir, fmt.Sprintf("owner-token-%d", len(ownerCreds)))
		write(t, path, token)
		ownerCreds = append(ownerCreds, serviceauth.Credential{ID: filepath.Base(path), PoolID: poolID, Roles: []string{role}, Resources: []string{"ownership"}, TokenSHA256: digest, ExpiresAt: time.Now().Add(time.Hour)})
		return ownership.Config{URL: ownerURL, PoolID: poolID, TokenFile: path, CAFile: ca}
	}
	sourceOwner := ownerConfig(sourcePool, "ownership-controller")
	targetOwner := ownerConfig(targetPool, "ownership-controller")
	brokerOwner := ownerConfig(sourcePool, "ownership-reader")
	fixtureJSON(t, ownerAuth, serviceauth.File{Credentials: ownerCreds})
	p.run(os.Environ(), "../pool-controller", "./cmd/livepeer-pool-ownership", "--listen", strings.TrimPrefix(ownerURL, "https://"), "--data-dir", filepath.Join(p.dir, "ownership"), "--service-auth-file", ownerAuth, "--tls-cert", ca, "--tls-key", key)
	sourceClient, err := ownership.NewClient(sourceOwner)
	if err != nil {
		t.Fatal(err)
	}
	targetClient, err := ownership.NewClient(targetOwner)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	eventually(t, "ownership claim", 30*time.Second, func() error {
		_, err := sourceClient.Claim(ctx, ownership.Request{DeviceID: "gpu-a", EnrollmentID: hostID, MemberWallet: wallet})
		return err
	})
	if _, err = sourceClient.Claim(ctx, ownership.Request{DeviceID: "gpu-b", EnrollmentID: hostID, MemberWallet: wallet}); err != nil {
		t.Fatal(err)
	}
	targetClaim := func() (ownership.Record, error) {
		return targetClient.Claim(ctx, ownership.Request{DeviceID: "gpu-a", EnrollmentID: "target-host", MemberWallet: wallet, ExpectedGeneration: 1})
	}
	if _, err = targetClaim(); err == nil {
		t.Fatal("target claimed active source")
	}

	brokerPort := freePort(t)
	brokerURL := fmt.Sprintf("http://127.0.0.1:%d", brokerPort)
	brokerProxy := tlsProcessProxy(t, brokerURL)
	brokerCA := filepath.Join(p.dir, "broker-ca.pem")
	write(t, brokerCA, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: brokerProxy.Certificate().Raw})))
	brokerToken, brokerDigest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	brokerTokenFile := filepath.Join(p.dir, "broker-token")
	write(t, brokerTokenFile, brokerToken)
	brokerAuth := filepath.Join(p.dir, "broker-auth.json")
	fixtureJSON(t, brokerAuth, serviceauth.File{Credentials: []serviceauth.Credential{{ID: "controller", PoolID: sourcePool, Roles: []string{"controller"}, Resources: []string{"transfer-broker"}, TokenSHA256: brokerDigest, ExpiresAt: time.Now().Add(time.Hour)}}})
	source := revenue.Source{PoolID: sourcePool, SourceID: "0x" + strings.Repeat("2", 64), BrokerID: "transfer-broker", ChainID: 42161, Payee: orchAddress, URL: brokerProxy.URL}
	brokerDir := filepath.Join(p.dir, "broker")
	if err = os.Mkdir(brokerDir, 0700); err != nil {
		t.Fatal(err)
	}
	settled := filepath.Join(p.dir, "work-settled")
	brokerFixture := filepath.Join(p.dir, "broker-fixture.json")
	fixtureJSON(t, brokerFixture, map[string]any{"Dir": brokerDir, "Listen": fmt.Sprintf("127.0.0.1:%d", brokerPort), "PoolID": sourcePool, "SourceID": source.SourceID, "BrokerID": source.BrokerID, "HostID": hostID, "Wallet": wallet, "Token": attachToken, "AuthFile": brokerAuth, "Settled": settled, "Ownership": brokerOwner})
	brokerProcess := p.runTestFixture("../capability-broker", "./internal/server", "TestRegionalTransferBrokerProcess", "REGIONAL_E2E_TRANSFER_BROKER", brokerFixture)
	p.waitHealthy(brokerURL+"/test/health", "transfer broker")
	dispatch := func(device string, want int) {
		t.Helper()
		code, raw := p.do("GET", brokerURL+"/test/dispatch/"+device, "", "")
		if code != want {
			t.Fatalf("dispatch %s: %d %s", device, code, raw)
		}
	}
	dispatch("gpu-a", 204)
	dispatch("gpu-b", 204)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer := memberauth.Signer{Issuer: "https://portal.example", KeyID: "fixture", PrivateKey: priv}
	trust := filepath.Join(p.dir, "member-trust.json")
	fixtureJSON(t, trust, memberauth.Trust{Issuer: signer.Issuer, Keys: []memberauth.PublicKey{{ID: signer.KeyID, PublicKey: base64.StdEncoding.EncodeToString(pub), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}}})
	catalog := filepath.Join(p.dir, "catalog")
	if err = os.Mkdir(catalog, 0700); err != nil {
		t.Fatal(err)
	}
	template := strings.Replace(fixtureTemplate, "image: { nvidia: example.invalid/e2e-chat:1 }", "image: { nvidia: alpine:3.20 }\n  gpu: false\n  command: [sleep, '3600']", 1)
	write(t, filepath.Join(catalog, "test.yaml"), template)
	hash := sha256.Sum256([]byte(agentToken))
	var hardware, assignments []map[string]any
	for _, device := range []string{"gpu-a", "gpu-b"} {
		hardware = append(hardware, map[string]any{"id": device, "gpu_uuid": device, "gpu_model": e2eGPUModel, "enrollment_id": hostID, "member_eth_address": wallet, "state": "active", "ownership_generation": 1})
		assignments = append(assignments, map[string]any{"id": device, "hardware_unit_id": device, "host_enrollment_id": hostID, "template_id": "e2e-chat", "state": "active"})
	}
	provision := filepath.Join(p.dir, "provision.json")
	fixtureJSON(t, provision, map[string]any{"DataDir": data, "Version": "transfer-v1", "Output": filepath.Join(p.dir, "identity.json"), "Wallet": wallet, "Sources": []revenue.Source{source}, "Enrollments": []map[string]any{{"id": hostID, "pool_id": sourcePool, "member_eth_address": wallet, "enrollment_token_hash": hex.EncodeToString(hash[:]), "broker_session_credential": attachToken, "status": "active", "terms_version": "transfer-v1"}}, "Hardware": hardware, "Assignments": assignments})
	runOfflineFixture(t, "../pool-controller", "./internal/repo", "TestRegionalE2EProvisionTerms", "REGIONAL_E2E_PROVISION", provision)
	controlPort := freePort(t)
	p.controlURL = fmt.Sprintf("http://127.0.0.1:%d", controlPort)
	adminToken, adminDigest, err := serviceauth.GenerateToken()
	if err != nil {
		t.Fatal(err)
	}
	controllerAuth := filepath.Join(p.dir, "controller-auth.json")
	fixtureJSON(t, controllerAuth, serviceauth.File{Credentials: []serviceauth.Credential{{ID: "operator", PoolID: sourcePool, Roles: []string{"pool-admin"}, Resources: []string{"controller"}, TokenSHA256: adminDigest, ExpiresAt: time.Now().Add(time.Hour)}}})
	// Lose the first successful release reply after the destination claims.
	// Real cross-pool reads redact member identity; completion must replay the
	// source's durable release receipt rather than depend on target-private data.
	ownerTarget, _ := url.Parse(ownerURL)
	releaseProxy := httputil.NewSingleHostReverseProxy(ownerTarget)
	ownerHTTP, err := serviceauth.HTTPSClientWithCAFile(ownerURL, sourcePool, sourceOwner.TokenFile, ca)
	if err != nil {
		t.Fatal(err)
	}
	// The ordinary TLS transport here adds the source pool credential; the
	// controller already presents that same scoped identity to this proxy.
	releaseProxy.Transport = ownerHTTP.Transport
	var lostRelease atomic.Bool
	releaseProxy.ModifyResponse = func(response *http.Response) error {
		if response.Request.URL.Path == "/ownership/v1/release" && response.StatusCode == 200 && lostRelease.CompareAndSwap(false, true) {
			if _, err := targetClaim(); err != nil {
				return fmt.Errorf("fixture destination claim: %w", err)
			}
			response.Body.Close()
			response.StatusCode = 502
			response.Body = io.NopCloser(strings.NewReader("fixture lost release response"))
			response.ContentLength = -1
			response.Header.Del("Content-Length")
		}
		return nil
	}
	faultyOwner := httptest.NewTLSServer(releaseProxy)
	t.Cleanup(faultyOwner.Close)
	faultCA := filepath.Join(p.dir, "ownership-proxy-ca.pem")
	write(t, faultCA, string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: faultyOwner.Certificate().Raw})))
	controllerOwner := sourceOwner
	controllerOwner.URL = faultyOwner.URL
	controllerOwner.CAFile = faultCA
	controllerConfig := filepath.Join(p.dir, "controller.json")
	fixtureJSON(t, controllerConfig, map[string]any{"identity": map[string]any{"orch_eth_address": orchAddress}, "listen": map[string]any{"paid": fmt.Sprintf("127.0.0.1:%d", controlPort), "metrics": fmt.Sprintf("127.0.0.1:%d", freePort(t))}, "service_auth_file": controllerAuth, "member_issuer_trust_file": trust, "template_catalog_dir": catalog, "ownership": controllerOwner, "bootstrap": map[string]any{"brokers": []map[string]any{{"name": source.BrokerID, "admin_url": source.URL, "auth": map[string]any{"method": "scoped", "pool_id": sourcePool, "token_file": brokerTokenFile, "ca_file": brokerCA}}}}})
	startController := func() *exec.Cmd {
		p.run(os.Environ(), "../pool-controller", "./cmd/livepeer-pool-controller", "serve", "--config", controllerConfig, "--data-dir", data)
		p.waitHealthy(p.controlURL+"/public/v1/summary", "transfer controller")
		return p.procs[len(p.procs)-1]
	}
	controllerProcess := startController()

	// Use real Docker Compose to make source and sibling containers exist.
	sum := sha256.Sum256([]byte(hostID))
	network := "livepeer-member-" + fmt.Sprintf("%x", sum[:8])
	project := "livepeer-runners-" + fmt.Sprintf("%x", sum[:8])
	transferDocker(t, "network", "create", network)
	t.Cleanup(func() {
		ids, _ := exec.Command("docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+project).Output()
		for _, id := range strings.Fields(string(ids)) {
			_ = exec.Command("docker", "rm", "-f", id).Run()
		}
		_ = exec.Command("docker", "network", "rm", network).Run()
	})
	agentFixture := filepath.Join(p.dir, "agent.json")
	composePath := filepath.Join(p.dir, "runners.compose.yaml")
	apply := func(suppress bool) {
		fixtureJSON(t, agentFixture, map[string]any{"URL": p.controlURL, "EnrollmentID": hostID, "Token": agentToken, "ComposePath": composePath, "ReportPath": filepath.Join(p.dir, "apply.json"), "SuppressReport": suppress})
		runOfflineFixture(t, "../pool-member-agent", "./internal/desiredstate", "TestRegionalTransferAgentApply", "REGIONAL_E2E_AGENT_APPLY", agentFixture)
	}
	container := func(device string) string {
		return transferDocker(t, "ps", "-q", "--filter", "label=com.docker.compose.project="+project, "--filter", "label=com.docker.compose.service=runner-"+device)
	}
	apply(false)
	initialA, initialB := container("gpu-a"), container("gpu-b")
	if initialA == "" || initialB == "" {
		t.Fatal("agent did not start both containers")
	}
	callMember := func(method, path, body string) (int, []byte) {
		token, err := signer.Sign(wallet, sourcePool, strings.Repeat("a", 64), time.Now(), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequest(method, p.controlURL+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Origin", p.controlURL)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode, readAll(t, resp)
	}
	workDone := make(chan error, 1)
	go func() {
		client := &http.Client{Timeout: 2 * time.Minute}
		response, err := client.Get(brokerURL + "/test/work")
		if err == nil {
			response.Body.Close()
			if response.StatusCode != 204 {
				err = fmt.Errorf("work returned %d", response.StatusCode)
			}
		}
		workDone <- err
	}()
	eventually(t, "in-flight synthetic work request", 10*time.Second, func() error { _, err := os.Stat(settled + ".inflight"); return err })
	body, _ := json.Marshal(map[string]string{"pool_id": sourcePool, "destination_pool_id": targetPool, "reason": "local acceptance transfer"})
	code, raw := callMember("POST", "/member/v1/hardware/gpu-a/transfer", string(body))
	if code != 202 {
		t.Fatalf("begin: %d %s", code, raw)
	}
	phase := func() string {
		code, raw := callMember("GET", "/member/v1/device-transfers", "")
		if code != 200 {
			t.Fatalf("transfers %d %s", code, raw)
		}
		var result struct {
			Transfers []struct {
				Phase string `json:"phase"`
			} `json:"transfers"`
		}
		decode(t, raw, &result)
		if len(result.Transfers) != 1 {
			t.Fatal(string(raw))
		}
		return result.Transfers[0].Phase
	}
	if phase() != "draining" {
		t.Fatal("active authorization did not hold drain")
	}
	dispatch("gpu-a", 409)
	dispatch("gpu-b", 204)
	apply(false)
	if container("gpu-a") != initialA || container("gpu-b") != initialB {
		t.Fatal("drain interrupted existing containers")
	}
	if _, err = targetClaim(); err == nil {
		t.Fatal("target claimed while draining")
	}
	stopRegionalProcess(t, controllerProcess)
	controllerProcess = startController()
	if phase() != "draining" {
		t.Fatal("restart lost pending work fence")
	}
	write(t, settled, "synthetic work completed\n")
	select {
	case err := <-workDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight work did not finish")
	}
	eventually(t, "source revoked and awaiting agent stop", 30*time.Second, func() error {
		if got := phase(); got != "stopping" {
			return fmt.Errorf("phase %s", got)
		}
		return nil
	})
	if _, err = targetClaim(); err == nil {
		t.Fatal("target claimed before actual stop")
	}
	apply(true) // Deliberately lose the acknowledgment after actual Docker stop.
	if container("gpu-a") != "" || container("gpu-b") != initialB {
		t.Fatal("actual stop failed or interrupted sibling")
	}
	if phase() != "stopping" {
		t.Fatal("release without stop acknowledgment")
	}
	if _, err = targetClaim(); err == nil {
		t.Fatal("target claimed without revision-bound acknowledgment")
	}
	stopRegionalProcess(t, controllerProcess)
	controllerProcess = startController()
	apply(false) // Agent restart replays the same desired revision and confirms stop.
	eventually(t, "fenced ownership release", 30*time.Second, func() error {
		if got := phase(); got != "released" {
			return fmt.Errorf("phase %s", got)
		}
		return nil
	})
	target, err := targetClient.Get(ctx, "gpu-a")
	if err != nil || target.Generation != 2 {
		t.Fatalf("target claim: %+v %v", target, err)
	}
	if !lostRelease.Load() {
		t.Fatal("release response fault was not exercised")
	}
	hidden, err := sourceClient.Get(ctx, "gpu-a")
	if err != nil || hidden.MemberWallet != "" || hidden.EnrollmentID != "" {
		t.Fatal("source can read destination private ownership", hidden, err)
	}
	if _, err = sourceClient.Claim(ctx, ownership.Request{DeviceID: "gpu-a", EnrollmentID: hostID, MemberWallet: wallet, ExpectedGeneration: 1}); err == nil {
		t.Fatal("stale source reclaimed generation")
	}
	dispatch("gpu-a", 409)
	dispatch("gpu-b", 204)
	stopRegionalProcess(t, brokerProcess)
	p.runTestFixture("../capability-broker", "./internal/server", "TestRegionalTransferBrokerProcess", "REGIONAL_E2E_TRANSFER_BROKER", brokerFixture)
	p.waitHealthy(brokerURL+"/test/health", "restarted transfer broker")
	dispatch("gpu-a", 409)
	dispatch("gpu-b", 204)
	if container("gpu-b") != initialB {
		t.Fatal("sibling execution changed during regional transfer")
	}
	_ = adminToken // Reserved fixture operator credential; member initiates the transfer.
}
