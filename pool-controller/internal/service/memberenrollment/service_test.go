package memberenrollment

import (
	"archive/zip"
	"bytes"
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
				Image:       "runner-chat:latest",
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
	if !bytes.Contains(envBody, []byte("POOL_ENROLLMENT_TOKEN_FILE=/run/livepeer/enrollment-token")) {
		t.Fatalf(".env missing token file: %s", string(envBody))
	}
	if !bytes.Contains(envBody, []byte("POOL_BROKER_QUIC_ADDR=broker.example.com:8443")) {
		t.Fatalf(".env missing broker quic addr: %s", string(envBody))
	}
	if !bytes.Contains(envBody, []byte("POOL_BROKER_SESSION_CREDENTIAL=")) {
		t.Fatalf(".env missing broker session credential: %s", string(envBody))
	}
	// The bundle no longer names the runners. It ships the agent, and
	// the agent asks the pool what to run — so a placement change does
	// not stale the bundle a member already downloaded.
	if bytes.Contains(envBody, []byte("POOL_WORKER_BACKENDS")) {
		t.Fatalf(".env still declares a static runner set: %s", string(envBody))
	}
	composeBody := zipFileBody(t, zr, "docker-compose.yaml")
	if !bytes.Contains(composeBody, []byte("gpus: all")) {
		t.Fatalf("compose missing gpu access: %s", string(composeBody))
	}
	// The agent writes runners.compose.yaml itself; the bundle includes
	// it optionally so a first boot with no placements still starts.
	if !bytes.Contains(composeBody, []byte("runners.compose.yaml")) {
		t.Fatalf("compose does not include the agent-written runner file: %s", string(composeBody))
	}
	if !bytes.Contains(composeBody, []byte("/var/run/docker.sock")) {
		t.Fatalf("agent cannot start runners without the docker socket: %s", string(composeBody))
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
