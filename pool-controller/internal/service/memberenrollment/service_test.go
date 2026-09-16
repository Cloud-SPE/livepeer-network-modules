package memberenrollment

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/crypto"
)

func TestServiceIssueAndVerifyNonce(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	svc := NewWithClock(stateRepo, func() time.Time { return now })
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()

	issued, err := svc.IssueNonce(NonceIssueRequest{EthAddress: addr})
	if err != nil {
		t.Fatalf("IssueNonce() error = %v", err)
	}
	sig, err := crypto.Sign(accounts.TextHash([]byte(issued.Message)), key)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	sig[64] += 27

	verified, err := svc.VerifyNonce(VerifyRequest{
		NonceID:      issued.NonceID,
		SignatureHex: "0x" + bytesToHex(sig),
		DisplayName:  "member-a",
	})
	if err != nil {
		t.Fatalf("VerifyNonce() error = %v", err)
	}
	if verified.Member.EthAddress != addr {
		t.Fatalf("verified address = %s, want %s", verified.Member.EthAddress, addr)
	}
	if verified.Member.DisplayName != "member-a" {
		t.Fatalf("display name = %q", verified.Member.DisplayName)
	}

	if _, err := svc.VerifyNonce(VerifyRequest{NonceID: issued.NonceID, SignatureHex: "0x" + bytesToHex(sig)}); err == nil {
		t.Fatal("VerifyNonce() reuse succeeded; want error")
	}
}

func TestServiceCreateEnrollmentAndRenderBundle(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	member := types.PoolMember{
		ID:         "0x0000000000000000000000000000000000000001",
		EthAddress: "0x0000000000000000000000000000000000000001",
		PayoutMode: "eth",
	}
	if err := stateRepo.PutPoolMember(member); err != nil {
		t.Fatalf("PutPoolMember() error = %v", err)
	}

	svc := New(stateRepo)
	created, err := svc.CreateEnrollment(CreateEnrollmentRequest{
		MemberEthAddress: member.EthAddress,
		HostLabel:        "rig-a",
	})
	if err != nil {
		t.Fatalf("CreateEnrollment() error = %v", err)
	}
	if created.Token == "" {
		t.Fatal("CreateEnrollment() returned empty token")
	}
	if created.Enrollment.EnrollmentTokenHash != HashToken(created.Token) {
		t.Fatalf("token hash mismatch")
	}

	raw, err := RenderBundleZip(BundleInput{
		ControllerURL:  "http://controller",
		BrokerURL:      "https://broker",
		BrokerQUICAddr: "broker.example.com:8443",
		Enrollment:     created.Enrollment,
		Token:          created.Token,
		Assignments: []types.TemplateAssignment{{
			ID:         "assign-chat-1",
			TemplateID: "chat-4090",
		}},
		Templates: []templates.Template{{
			ID: "chat-4090",
			RunnerCompose: templates.RunnerCompose{
				Image:       map[string]string{"nvidia": "runner-chat:latest"},
				InternalURL: "http://chat-runner:9000",
				Env:         map[string]string{"QUANT": "fp8", "MODEL": "small"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("RenderBundleZip() error = %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatalf("zip.NewReader() error = %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, name := range []string{"README.md", ".env", "docker-compose.yaml", "update.sh", "enrollment-token", "pool-member-agent.yaml"} {
		if !names[name] {
			t.Fatalf("bundle missing %s; names=%v", name, names)
		}
	}
	envBody := zipFileBody(t, zr, ".env")
	if !bytes.Contains(envBody, []byte("POOL_ENROLLMENT_TOKEN_FILE=/workspace/enrollment-token")) {
		t.Fatalf(".env missing token file: %s", string(envBody))
	}
	// The agent reads LIVEPEER_* to attach and POOL_* to reach the
	// controller. Emitting only the POOL_ names — which is what this
	// bundle used to do — leaves a member running `docker compose up`
	// against an agent that has no broker URL and no credential.
	if !bytes.Contains(envBody, []byte("LIVEPEER_BROKER_QUIC_ADDR=broker.example.com:8443")) {
		t.Fatalf(".env missing broker quic addr: %s", string(envBody))
	}
	if !bytes.Contains(envBody, []byte("LIVEPEER_BROKER_URL=https://broker")) {
		t.Fatalf(".env missing broker url: %s", string(envBody))
	}
	if !bytes.Contains(envBody, []byte("LIVEPEER_ATTACH_CREDENTIAL=")) {
		t.Fatalf(".env missing the attach credential: %s", string(envBody))
	}
	// The host id must BE the enrolment id, or the GPUs this agent
	// reports attach to an enrolment that does not exist.
	if !bytes.Contains(envBody, []byte("LIVEPEER_HOST_ID="+created.Enrollment.ID)) {
		t.Fatalf(".env host id is not the enrolment id: %s", string(envBody))
	}

	// The bundle no longer names the runners. It ships the agent, and
	// the agent asks the pool what to run — so a placement change does
	// not stale the bundle a member already downloaded.
	if bytes.Contains(envBody, []byte("POOL_WORKER_BACKENDS")) {
		t.Fatalf(".env still declares a static runner set: %s", string(envBody))
	}
	composeBody := zipFileBody(t, zr, "docker-compose.yaml")
	if bytes.Contains(composeBody, []byte("gpus: all")) {
		t.Fatalf("base compose requires NVIDIA on every host: %s", string(composeBody))
	}
	// First boot must not depend on an absent generated file, or share the
	// runner project (whose remove-orphans could kill the agent).
	if bytes.Contains(composeBody, []byte("include:")) || !bytes.Contains(composeBody, []byte("name: livepeer-agent-")) || !bytes.Contains(composeBody, []byte("name: livepeer-member-")) {
		t.Fatalf("invalid bootstrap project/network: %s", composeBody)
	}
	if bytes.Contains(composeBody, []byte("enrollment-token:ro")) {
		t.Fatal("token rotation needs a writable directory mount")
	}
	if !bytes.Contains(composeBody, []byte("/var/run/docker.sock")) {
		t.Fatalf("agent cannot start runners without the docker socket: %s", string(composeBody))
	}
	if !bytes.Contains(composeBody, []byte("/var/lib/livepeer-resource-admission:/var/lib/livepeer-resource-admission")) {
		t.Fatalf("agent cannot initialize host-shared admission state: %s", string(composeBody))
	}
	if bytes.Contains(composeBody, []byte("runner_assign_chat_1:")) {
		t.Fatalf("bundle still ships a per-placement service: %s", string(composeBody))
	}
}

func TestServiceRejectsExpiredNonce(t *testing.T) {
	stateRepo, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = stateRepo.Close() }()

	now := time.Date(2026, 6, 3, 12, 0, 0, 0, time.UTC)
	svc := NewWithClock(stateRepo, func() time.Time { return now })
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	addr := crypto.PubkeyToAddress(key.PublicKey).Hex()
	issued, err := svc.IssueNonce(NonceIssueRequest{EthAddress: addr})
	if err != nil {
		t.Fatalf("IssueNonce() error = %v", err)
	}
	nonce, err := stateRepo.GetMemberNonce(issued.NonceID)
	if err != nil {
		t.Fatalf("GetMemberNonce() error = %v", err)
	}
	nonce.ExpiresAt = now.Add(-time.Second)
	if err := stateRepo.PutMemberNonce(nonce); err != nil {
		t.Fatalf("PutMemberNonce() error = %v", err)
	}
	sig, err := crypto.Sign(accounts.TextHash([]byte(issued.Message)), key)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	_, err = svc.VerifyNonce(VerifyRequest{NonceID: issued.NonceID, SignatureHex: "0x" + bytesToHex(sig)})
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("VerifyNonce() error = %v, want expired", err)
	}
}

func bytesToHex(b []byte) string {
	const chars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = chars[v>>4]
		out[i*2+1] = chars[v&0x0f]
	}
	return string(out)
}

func zipFileBody(t *testing.T, zr *zip.Reader, name string) []byte {
	t.Helper()
	for _, f := range zr.File {
		if f.Name != name {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip file %s: %v", name, err)
		}
		defer func() { _ = rc.Close() }()
		var buf bytes.Buffer
		if _, err := buf.ReadFrom(rc); err != nil {
			t.Fatalf("read zip file %s: %v", name, err)
		}
		return buf.Bytes()
	}
	t.Fatalf("zip file %s not found", name)
	return nil
}

// Render the actual first-boot archive with Compose, not just string assertions.
// No containers are started and all credentials are synthetic.
func TestBundleComposeFirstBoot(t *testing.T) {
	if os.Getenv("CHECK_COMPOSE") != "1" {
		t.Skip("set CHECK_COMPOSE=1 to validate using Docker Compose")
	}
	raw, err := RenderBundleZip(BundleInput{ControllerURL: "https://members.example.com", BrokerURL: "https://broker.example.com", Token: "test-token", Enrollment: types.HostEnrollment{ID: "host-test", BrokerSessionCredential: "test-credential"}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range zr.File {
		if err := os.WriteFile(filepath.Join(dir, f.Name), zipFileBody(t, zr, f.Name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("docker", "compose", "--env-file", filepath.Join(dir, ".env"), "-f", filepath.Join(dir, "docker-compose.yaml"), "config", "--format", "json")
	cmd.Env = append(os.Environ(), "REGISTRY=tztcloud", "TAG=v2.0.0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("first boot Compose invalid: %v: %s", err, out)
	}
	var model struct {
		Name     string `json:"name"`
		Services map[string]struct {
			Image string `json:"image"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &model); err != nil {
		t.Fatal(err)
	}
	if len(model.Services) != 1 || model.Services["pool_member_agent"].Image != "tztcloud/livepeer-pool-member-agent:v2.0.0" {
		t.Fatalf("unexpected bootstrap: %s", out)
	}
	if model.Name != "livepeer-agent-"+bundleNetworkID(BundleInput{Enrollment: types.HostEnrollment{ID: "host-test"}}) {
		t.Fatal(model.Name)
	}
}

func TestRegionalEnrollmentRequiresAcceptanceAndRefreshesExistingHostTerms(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	wallet := "0x0000000000000000000000000000000000000001"
	if err := st.PutPoolMember(types.PoolMember{ID: wallet, EthAddress: wallet}); err != nil {
		t.Fatal(err)
	}
	term := types.RegionalTerms{PoolID: st.PoolID(), Version: "v1", EffectiveRound: 100, WindowRounds: 14, CommissionBPS: 1000, ParticipationRules: "regional terms", ZeroWorkToOperator: true, RoundingToOperator: true}
	if err := st.PutRegionalTerms(term); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	req := CreateEnrollmentRequest{MemberEthAddress: wallet, HostLabel: "member host"}
	if _, err := svc.CreateEnrollment(req); err == nil {
		t.Fatal("unaccepted regional enrollment admitted")
	}
	accepted, err := st.AcceptRegionalTerms(st.PoolID(), wallet, "v1")
	if err != nil {
		t.Fatal(err)
	}
	enrolled, err := svc.CreateEnrollment(req)
	if err != nil || enrolled.Enrollment.TermsVersion != "v1" {
		t.Fatalf("enrollment %+v %v", enrolled, err)
	}
	term.Version = "v2"
	term.EffectiveRound = 114
	term.CommissionBPS = 2000
	if err := st.PutRegionalTerms(term); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEnrollment(req); err == nil {
		t.Fatal("new terms did not require acceptance")
	}
	old, err := st.GetHostEnrollment(enrolled.Enrollment.ID)
	if err != nil || old.TermsVersion != "v1" {
		t.Fatal("publication silently accepted new terms")
	}
	if _, err := st.AcceptRegionalTerms(st.PoolID(), wallet, "v2"); err != nil {
		t.Fatal(err)
	}
	refreshed, err := st.GetHostEnrollment(enrolled.Enrollment.ID)
	if err != nil || refreshed.TermsVersion != "v2" {
		t.Fatalf("host terms not refreshed %+v %v", refreshed, err)
	}
	replay, err := st.AcceptRegionalTerms(st.PoolID(), wallet, "v1")
	if err != nil || !replay.AcceptedAt.Equal(accepted.AcceptedAt) {
		t.Fatal("historical acceptance changed")
	}
	refreshed, _ = st.GetHostEnrollment(enrolled.Enrollment.ID)
	if refreshed.TermsVersion != "v2" {
		t.Fatal("old acceptance rolled back host grant")
	}
	old.LastSeenAt = time.Now().UTC()
	if err := st.PutHostEnrollment(old); err != nil {
		t.Fatal(err)
	}
	refreshed, _ = st.GetHostEnrollment(enrolled.Enrollment.ID)
	if refreshed.TermsVersion != "v2" {
		t.Fatal("stale host status rolled back accepted terms")
	}
	member, err := st.GetPoolMember(wallet)
	if err != nil {
		t.Fatal(err)
	}
	member.Status = types.MemberStatusSuspended
	if err := st.PutPoolMember(member); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateEnrollment(req); err == nil {
		t.Fatal("suspended member enrolled with old acceptance")
	}

}

func TestWalletSignInDoesNotLiftRegionalSuspension(t *testing.T) {
	st, err := repo.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	wallet := crypto.PubkeyToAddress(key.PublicKey).Hex()
	if err := st.PutPoolMember(types.PoolMember{EthAddress: wallet, Status: types.MemberStatusSuspended}); err != nil {
		t.Fatal(err)
	}
	svc := New(st)
	nonce, err := svc.IssueNonce(NonceIssueRequest{EthAddress: wallet})
	if err != nil {
		t.Fatal(err)
	}
	signature, err := crypto.Sign(accounts.TextHash([]byte(nonce.Message)), key)
	if err != nil {
		t.Fatal(err)
	}
	result, err := svc.VerifyNonce(VerifyRequest{NonceID: nonce.NonceID, SignatureHex: fmt.Sprintf("0x%x", signature)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Member.Status != types.MemberStatusSuspended {
		t.Fatal("wallet proof lifted operator suspension")
	}
	if _, err := svc.CreateEnrollment(CreateEnrollmentRequest{MemberEthAddress: wallet}); err == nil {
		t.Fatal("suspended member enrolled")
	}
}

func TestRegionalFleetBundle(t *testing.T) {
	image := "registry.example/agent@sha256:" + strings.Repeat("a", 64)
	for _, urls := range [][]string{{"https://eu-transcode.example"}, {"https://us-transcode.example", "https://audio.example", "https://llm.example"}} {
		raw, err := RenderBundleZip(BundleInput{ControllerURL: "https://members.example", BrokerURLs: urls, MemberAgentImage: image, Token: "synthetic", Enrollment: types.HostEnrollment{ID: "host-regional", PoolID: "pool-regional", BrokerSessionCredential: "synthetic-attach"}})
		if err != nil {
			t.Fatal(err)
		}
		zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
		if err != nil {
			t.Fatal(err)
		}
		env := string(zipFileBody(t, zr, ".env"))
		compose := string(zipFileBody(t, zr, "docker-compose.yaml"))
		if !strings.Contains(env, "LIVEPEER_BROKER_URLS="+strings.Join(urls, ",")) || !strings.Contains(compose, "image: "+image) || strings.Count(compose, "    image:") != 1 {
			t.Fatalf("fleet/pinned agent missing: %s %s", env, compose)
		}
		pair := string(zipFileBody(t, zr, "agent-credentials.json"))
		if !strings.Contains(pair, "synthetic-attach") {
			t.Fatal("missing durable credential pair")
		}
	}
}
