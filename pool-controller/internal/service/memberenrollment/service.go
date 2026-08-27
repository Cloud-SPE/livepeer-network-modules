package memberenrollment

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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
		"POOL_WORKER_BACKENDS=" + workerBackendsEnv(input) + "\n" +
		"POOL_ENROLLMENT_TOKEN_FILE=/run/livepeer/enrollment-token\n"
}

func bundleReadme(input BundleInput) string {
	return "# Livepeer Pool member host\n\n" +
		"Run `docker compose up -d` from this directory. The member agent will connect outbound to the Pool broker and report visible GPUs.\n\n" +
		"Enrollment: `" + input.Enrollment.ID + "`\n"
}

func bundleCompose(input BundleInput) string {
	out := "services:\n" +
		"  pool_member_agent:\n" +
		"    image: ${POOL_MEMBER_AGENT_IMAGE:-livepeer-pool-member-agent:dev}\n" +
		"    restart: unless-stopped\n" +
		"    gpus: all\n" +
		"    env_file: .env\n" +
		"    volumes:\n" +
		"      - ./enrollment-token:/run/livepeer/enrollment-token:ro\n" +
		"      - ./pool-member-agent.yaml:/etc/livepeer/pool-member-agent.yaml:ro\n"
	for _, assignment := range input.Assignments {
		template, ok := templateByID(input.Templates, assignment.TemplateID)
		if !ok {
			continue
		}
		image := strings.TrimSpace(template.RunnerCompose.Image)
		if image == "" {
			continue
		}
		service := runnerServiceName(assignment.ID)
		out += "  " + service + ":\n" +
			"    image: " + image + "\n" +
			"    restart: unless-stopped\n" +
			"    gpus: all\n"
		if cmd := template.RunnerCompose.Command; len(cmd) > 0 {
			out += "    command:\n"
			for _, item := range cmd {
				out += "      - " + item + "\n"
			}
		}
		if env := template.RunnerCompose.Env; len(env) > 0 {
			// Sorted: a bundle is fetched repeatedly by the update
			// script, and map order would make every fetch look like a
			// change.
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out += "    environment:\n"
			for _, k := range keys {
				out += "      " + k + ": " + env[k] + "\n"
			}
		}
	}
	return out
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

func workerBackendsEnv(input BundleInput) string {
	var parts []string
	for _, assignment := range input.Assignments {
		template, ok := templateByID(input.Templates, assignment.TemplateID)
		if !ok {
			continue
		}
		internalURL := strings.TrimSpace(template.RunnerCompose.InternalURL)
		image := strings.TrimSpace(template.RunnerCompose.Image)
		// Include the backend when the template gives any way to reach a runner:
		// an explicit internal_url (operator runs their own runner; the bundle
		// ships nothing) or a shipped image (the bundle spins one up and the
		// agent talks to it at the default service URL). Skip only when neither
		// is present -- there is nothing to route to. This decouples backend
		// inclusion from shipping a runner image (bundleCompose still gates the
		// runner service on image).
		if internalURL == "" && image == "" {
			continue
		}
		if internalURL == "" {
			internalURL = "http://" + runnerServiceName(assignment.ID) + ":8080"
		}
		parts = append(parts, assignment.ID+"="+internalURL)
	}
	return strings.Join(parts, ",")
}

func templateByID(items []templates.Template, id string) (templates.Template, bool) {
	for _, item := range items {
		if item.ID == id {
			return item, true
		}
	}
	return templates.Template{}, false
}

func runnerServiceName(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	var b strings.Builder
	b.WriteString("runner_")
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.TrimRight(b.String(), "_")
}
