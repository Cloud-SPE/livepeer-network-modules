package memberenrollment

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/repo"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
	"github.com/ethereum/go-ethereum/accounts"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

const DefaultNonceTTL = 10 * time.Minute

type Service struct {
	repo *repo.StateRepo
	now  func() time.Time
}

type NonceIssueRequest struct {
	EthAddress string
}

type NonceIssueResult struct {
	NonceID    string    `json:"nonce_id"`
	EthAddress string    `json:"eth_address"`
	Message    string    `json:"message"`
	ExpiresAt  time.Time `json:"expires_at"`
}

type VerifyRequest struct {
	NonceID      string
	SignatureHex string
	DisplayName  string
	Contact      string
}

type VerifyResult struct {
	Member types.PoolMember `json:"member"`
}

type CreateEnrollmentRequest struct {
	MemberEthAddress string
	HostLabel        string
}

type CreateEnrollmentResult struct {
	Enrollment types.HostEnrollment `json:"enrollment"`
	Token      string               `json:"token"`
}

type BundleInput struct {
	ControllerURL  string
	BrokerURL      string
	BrokerQUICAddr string
	Enrollment     types.HostEnrollment
	Token          string
	Assignments    []types.TemplateAssignment
	Templates      []templates.Template
}

func New(stateRepo *repo.StateRepo) *Service {
	return &Service{repo: stateRepo, now: func() time.Time { return time.Now().UTC() }}
}

func NewWithClock(stateRepo *repo.StateRepo, now func() time.Time) *Service {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{repo: stateRepo, now: now}
}

func (s *Service) IssueNonce(req NonceIssueRequest) (NonceIssueResult, error) {
	addr, err := normalizeAddress(req.EthAddress)
	if err != nil {
		return NonceIssueResult{}, err
	}
	nonce, err := randomHex(16)
	if err != nil {
		return NonceIssueResult{}, err
	}
	now := s.now()
	nonceID := "nonce-" + nonce
	message := buildSignupMessage(addr, nonce, now.Add(DefaultNonceTTL))
	item := types.MemberNonce{
		ID:         nonceID,
		EthAddress: addr,
		Nonce:      nonce,
		Message:    message,
		ExpiresAt:  now.Add(DefaultNonceTTL),
		CreatedAt:  now,
	}
	if err := s.repo.PutMemberNonce(item); err != nil {
		return NonceIssueResult{}, err
	}
	return NonceIssueResult{
		NonceID:    nonceID,
		EthAddress: addr,
		Message:    message,
		ExpiresAt:  item.ExpiresAt,
	}, nil
}

func (s *Service) VerifyNonce(req VerifyRequest) (VerifyResult, error) {
	nonce, err := s.repo.GetMemberNonce(strings.TrimSpace(req.NonceID))
	if err != nil {
		return VerifyResult{}, err
	}
	now := s.now()
	if !nonce.UsedAt.IsZero() {
		return VerifyResult{}, errors.New("nonce has already been used")
	}
	if now.After(nonce.ExpiresAt) {
		return VerifyResult{}, errors.New("nonce has expired")
	}
	recovered, err := recoverPersonalSignAddress(nonce.Message, req.SignatureHex)
	if err != nil {
		return VerifyResult{}, err
	}
	if !strings.EqualFold(recovered, nonce.EthAddress) {
		return VerifyResult{}, fmt.Errorf("signature recovered %s, want %s", recovered, nonce.EthAddress)
	}
	if err := s.repo.MarkMemberNonceUsed(nonce.ID, now); err != nil {
		return VerifyResult{}, err
	}
	member := types.PoolMember{
		ID:          strings.ToLower(nonce.EthAddress),
		EthAddress:  nonce.EthAddress,
		DisplayName: strings.TrimSpace(req.DisplayName),
		Contact:     strings.TrimSpace(req.Contact),
		PayoutMode:  "eth",
		Status:      types.MemberStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if existing, err := s.repo.GetPoolMember(member.ID); err == nil {
		member.CreatedAt = existing.CreatedAt
		if member.DisplayName == "" {
			member.DisplayName = existing.DisplayName
		}
		if member.Contact == "" {
			member.Contact = existing.Contact
		}
	}
	if err := s.repo.PutPoolMember(member); err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Member: member}, nil
}

func (s *Service) CreateEnrollment(req CreateEnrollmentRequest) (CreateEnrollmentResult, error) {
	addr, err := normalizeAddress(req.MemberEthAddress)
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	if _, err := s.repo.GetPoolMember(strings.ToLower(addr)); err != nil {
		return CreateEnrollmentResult{}, fmt.Errorf("member must verify eth address before enrollment: %w", err)
	}
	token, err := randomHex(32)
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	now := s.now()
	enrollmentID := "host-" + token[:16]
	sessionCred, err := randomHex(32)
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	enrollment := types.HostEnrollment{
		ID:                      enrollmentID,
		MemberEthAddress:        addr,
		HostLabel:               strings.TrimSpace(req.HostLabel),
		EnrollmentTokenHash:     HashToken(token),
		BrokerSessionCredential: sessionCred,
		Status:                  types.HostEnrollmentPending,
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := s.repo.PutHostEnrollment(enrollment); err != nil {
		return CreateEnrollmentResult{}, err
	}
	return CreateEnrollmentResult{Enrollment: enrollment, Token: token}, nil
}

// Rotate issues a fresh enrollment token and broker session credential
// for an existing host, invalidating the old pair.
//
// Both secrets rotate together on purpose. They are handed to the same
// host at the same time and a member who rotates because one may have
// leaked has no way to know it was only one — rotating half would leave
// them believing they had recovered when they had not.
//
// The new token is returned once and stored only as a hash, so this is
// the sole moment it exists anywhere the member can read it.
func (s *Service) Rotate(enrollmentID string) (types.HostEnrollment, string, error) {
	enrollment, err := s.repo.GetHostEnrollment(strings.TrimSpace(enrollmentID))
	if err != nil {
		return types.HostEnrollment{}, "", err
	}
	switch enrollment.Status {
	case types.HostEnrollmentRevoked, types.HostEnrollmentRetired:
		// Rotating a dead enrollment would quietly revive it.
		return types.HostEnrollment{}, "", fmt.Errorf("enrollment %s is %s", enrollment.ID, enrollment.Status)
	}
	token, err := randomHex(32)
	if err != nil {
		return types.HostEnrollment{}, "", err
	}
	sessionCred, err := randomHex(32)
	if err != nil {
		return types.HostEnrollment{}, "", err
	}
	now := s.now()
	enrollment.EnrollmentTokenHash = HashToken(token)
	enrollment.BrokerSessionCredential = sessionCred
	enrollment.UpdatedAt = now
	if err := s.repo.PutHostEnrollment(enrollment); err != nil {
		return types.HostEnrollment{}, "", err
	}
	return enrollment, token, nil
}

// GetEnrollmentForToken validates a host enrollment bearer token and returns
// the matching enrollment. Enrollment tokens are host-side credentials; member
// dashboard actions should continue to use member authentication.
func (s *Service) GetEnrollmentForToken(enrollmentID, token string) (types.HostEnrollment, error) {
	enrollmentID = strings.TrimSpace(enrollmentID)
	tokenHash := HashToken(token)
	if enrollmentID == "" {
		return types.HostEnrollment{}, fmt.Errorf("enrollment_id is required")
	}
	if tokenHash == "" {
		return types.HostEnrollment{}, fmt.Errorf("enrollment token is required")
	}
	enrollment, err := s.repo.GetHostEnrollment(enrollmentID)
	if err != nil {
		return types.HostEnrollment{}, err
	}
	if enrollment.EnrollmentTokenHash != tokenHash {
		return types.HostEnrollment{}, fmt.Errorf("invalid enrollment token")
	}
	if enrollment.Status == types.HostEnrollmentRevoked || enrollment.Status == types.HostEnrollmentRetired {
		return types.HostEnrollment{}, fmt.Errorf("enrollment is not active")
	}
	return enrollment, nil
}

func RenderBundleZip(input BundleInput) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"README.md":              bundleReadme(input),
		".env":                   bundleEnv(input),
		"docker-compose.yaml":    bundleCompose(input),
		"update.sh":              bundleUpdateScript(),
		"enrollment-token":       input.Token + "\n",
		"pool-member-agent.yaml": bundleAgentConfig(input),
	}
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			_ = zw.Close()
			return nil, err
		}
		if _, err := w.Write([]byte(body)); err != nil {
			_ = zw.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func normalizeAddress(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if !common.IsHexAddress(addr) {
		return "", fmt.Errorf("invalid eth address %q", addr)
	}
	return common.HexToAddress(addr).Hex(), nil
}

func recoverPersonalSignAddress(message string, signatureHex string) (string, error) {
	sig, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(signatureHex), "0x"))
	if err != nil {
		return "", fmt.Errorf("decode signature: %w", err)
	}
	if len(sig) != 65 {
		return "", fmt.Errorf("signature must be 65 bytes")
	}
	if sig[64] >= 27 {
		sig[64] -= 27
	}
	digest := accounts.TextHash([]byte(message))
	pub, err := crypto.SigToPub(digest, sig)
	if err != nil {
		return "", fmt.Errorf("recover signature: %w", err)
	}
	return crypto.PubkeyToAddress(*pub).Hex(), nil
}

func buildSignupMessage(addr string, nonce string, expiresAt time.Time) string {
	return "Livepeer Pool signup\n" +
		"Address: " + addr + "\n" +
		"Nonce: " + nonce + "\n" +
		"Expires At: " + expiresAt.UTC().Format(time.RFC3339)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func bundleEnv(input BundleInput) string {
	return "POOL_CONTROLLER_URL=" + input.ControllerURL + "\n" +
		"POOL_BROKER_URL=" + input.BrokerURL + "\n" +
		"POOL_BROKER_QUIC_ADDR=" + input.BrokerQUICAddr + "\n" +
		"POOL_ENROLLMENT_ID=" + input.Enrollment.ID + "\n" +
		"POOL_MEMBER_ETH_ADDRESS=" + input.Enrollment.MemberEthAddress + "\n" +
		"POOL_BROKER_SESSION_CREDENTIAL=" + input.Enrollment.BrokerSessionCredential + "\n" +
		"POOL_ENROLLMENT_TOKEN_FILE=/run/livepeer/enrollment-token\n"
}

func bundleReadme(input BundleInput) string {
	return "# Livepeer Pool member host\n\n" +
		"Run `docker compose up -d` from this directory. That is the whole of it.\n\n" +
		"The agent connects outbound to the Pool broker, reports the GPUs it can\n" +
		"see, and asks the Pool what it should be running. Nothing needs to be\n" +
		"opened to the internet on this host.\n\n" +
		"## What the Pool runs here\n\n" +
		"The agent writes `runners.compose.yaml` and starts the containers the\n" +
		"Pool has placed on your GPUs. You can read that file at any time to see\n" +
		"exactly what is running and why — each service names the template and\n" +
		"the assignment it came from.\n\n" +
		"## What the Pool asks of your host\n\n" +
		"The agent mounts the Docker socket, because starting and stopping those\n" +
		"containers is its job. That is a real grant of privilege on this\n" +
		"machine and you should know you are making it. The agent starts only\n" +
		"images from the Pool's published template catalog, pinned to the GPUs\n" +
		"assigned to you.\n\n" +
		"## Leaving\n\n" +
		"`docker compose down` stops everything. To leave properly, retire the\n" +
		"host from the member portal first: your placements drain, in-flight\n" +
		"work finishes, and you stop being sent new jobs before the containers\n" +
		"go away.\n\n" +
		"Enrollment: `" + input.Enrollment.ID + "`\n"
}

// bundleCompose ships the AGENT and nothing else.
//
// It used to ship a service per placement, which meant the bundle went
// stale the moment the pool placed anything new: a member would have
// had to re-download and re-apply it for every change. The agent now
// pulls its desired state and writes runners.compose.yaml itself (plan
// 0044 §3.4), so the bundle is a bootstrap — the one thing that has to
// arrive out of band — and the runner set is live state.
//
// The generated file is included from here rather than merged into it,
// so `docker compose up` in this directory starts the agent and
// whatever the agent has decided should run alongside it.
func bundleCompose(input BundleInput) string {
	return "include:\n" +
		"  - path: ./runners.compose.yaml\n" +
		"    required: false\n" +
		"services:\n" +
		"  pool_member_agent:\n" +
		"    image: ghcr.io/cloud-spe/livepeer-pool-member-agent:latest\n" +
		"    restart: unless-stopped\n" +
		"    gpus: all\n" +
		"    env_file: .env\n" +
		"    volumes:\n" +
		"      - ./enrollment-token:/run/livepeer/enrollment-token:ro\n" +
		"      - ./pool-member-agent.yaml:/etc/livepeer/pool-member-agent.yaml:ro\n" +
		// The agent writes the runner compose file and drives docker,
		// so it needs the socket and a place to write. This is the
		// whole of what the pool asks of the host, and the member
		// README says so plainly rather than burying it.
		"      - /var/run/docker.sock:/var/run/docker.sock\n" +
		"      - ./:/workspace\n" +
		"    working_dir: /workspace\n"
}

func bundleUpdateScript() string {
	return "#!/usr/bin/env sh\n" +
		"set -eu\n" +
		": \"${POOL_CONTROLLER_URL:?POOL_CONTROLLER_URL is required}\"\n" +
		": \"${POOL_ENROLLMENT_ID:?POOL_ENROLLMENT_ID is required}\"\n" +
		"token=$(cat ./enrollment-token)\n" +
		"curl -fsS -H \"Authorization: Bearer ${token}\" \"${POOL_CONTROLLER_URL}/member/v1/enrollments/${POOL_ENROLLMENT_ID}/bundle\" -o bundle.zip\n" +
		"unzip -o bundle.zip\n" +
		"docker compose pull\n" +
		"docker compose up -d\n"
}

func bundleAgentConfig(input BundleInput) string {
	return "enrollment_id: " + input.Enrollment.ID + "\n" +
		"controller_url: " + input.ControllerURL + "\n" +
		"broker_url: " + input.BrokerURL + "\n" +
		"token_file: /run/livepeer/enrollment-token\n"
}
