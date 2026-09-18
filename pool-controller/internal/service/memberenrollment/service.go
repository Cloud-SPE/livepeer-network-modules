package memberenrollment

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	GPUUUIDs         []string
	MemberEthAddress string
	HostLabel        string
}

type CreateEnrollmentResult struct {
	Enrollment types.HostEnrollment `json:"enrollment"`
	Token      string               `json:"token"`
}

type BundleInput struct {
	ControllerURL    string
	BrokerURLs       []string
	MemberAgentImage string
	BrokerURL        string
	BrokerQUICAddr   string
	Enrollment       types.HostEnrollment
	Token            string
	Assignments      []types.TemplateAssignment
	Templates        []templates.Template
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
	message := buildSignupMessage(addr, nonce, now.Add(DefaultNonceTTL)) + "\nPool ID: " + s.repo.PoolID()
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
		PoolID:      s.repo.PoolID(),
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
	member, err = s.repo.RecordMemberAuthentication(member)
	if err != nil {
		return VerifyResult{}, err
	}
	return VerifyResult{Member: member}, nil
}

func (s *Service) CreateEnrollment(req CreateEnrollmentRequest) (CreateEnrollmentResult, error) {
	selected, err := normalizeSelectedGPUs(req.GPUUUIDs)
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	addr, err := normalizeAddress(req.MemberEthAddress)
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	member, err := s.repo.GetPoolMember(strings.ToLower(addr))
	if err != nil {
		return CreateEnrollmentResult{}, fmt.Errorf("member must verify eth address before enrollment: %w", err)
	}
	if member.Status != types.MemberStatusActive {
		return CreateEnrollmentResult{}, fmt.Errorf("member is not active")
	}

	terms, err := s.repo.ListRegionalTerms()
	if err != nil {
		return CreateEnrollmentResult{}, err
	}
	termsVersion := ""
	if len(terms) > 0 {
		termsVersion = terms[len(terms)-1].Version
		if _, err := s.repo.RequireTermsAcceptance(addr, termsVersion); err != nil {
			return CreateEnrollmentResult{}, err
		}
	} else {
		sources, err := s.repo.RevenueSources(nil)
		if err != nil {
			return CreateEnrollmentResult{}, err
		}
		if len(sources) > 0 {
			return CreateEnrollmentResult{}, fmt.Errorf("regional terms must be published before enrollment")
		}
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
		GPUUUIDs:                selected,
		CredentialGeneration:    1,
		TermsVersion:            termsVersion,
		PoolID:                  s.repo.PoolID(),
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
	enrollment, err = s.repo.RotateEnrollmentCredentials(enrollment.ID, enrollment.EnrollmentTokenHash, HashToken(token), sessionCred, now)
	if err != nil {
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
		"start.sh":               bundleStartScript(),
		"enrollment-token":       input.Token + "\n",
		"pool-member-agent.yaml": bundleAgentConfig(input),
	}
	if input.Enrollment.PoolID != "" {
		raw, err := json.Marshal(map[string]any{"pool_id": input.Enrollment.PoolID, "enrollment_id": input.Enrollment.ID, "enrollment_token": input.Token, "attach_credential": input.Enrollment.BrokerSessionCredential, "credential_generation": input.Enrollment.CredentialGeneration})
		if err != nil {
			return nil, err
		}
		files["agent-credentials.json"] = string(raw) + "\n"
	}
	for name, body := range files {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetMode(0644)
		if name == ".env" || name == "enrollment-token" || name == "agent-credentials.json" {
			header.SetMode(0600)
		}
		w, err := zw.CreateHeader(header)
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
	edgePort, rtmpsPort := "8443", "1936"
	if len(input.Enrollment.GPUUUIDs) > 0 {
		edgePort, rtmpsPort = "0", "0"
	}
	// Two families of name, and they are not interchangeable.
	//
	// LIVEPEER_* is what the agent reads to ATTACH to the broker; POOL_*
	// is what it reads to talk to this controller. The bundle used to
	// emit only the POOL_ names, so a member ran `docker compose up` and
	// the agent found no broker URL and no credential — a fresh bundle
	// could not attach at all.
	//
	// LIVEPEER_HOST_ID is the enrolment id on purpose. The broker relays
	// hardware keyed by the host id the agent declares, and the
	// controller stores GPUs against the enrolment; if the agent
	// invented its own id from the hostname, its cards would attach to
	// an enrolment that does not exist and placement would find nothing
	// to place on.
	return "POOL_CONTROLLER_URL=" + input.ControllerURL + "\n" +
		"POOL_ENROLLMENT_ID=" + input.Enrollment.ID + "\n" +
		"POOL_GPU_UUIDS=" + strings.Join(input.Enrollment.GPUUUIDs, ",") + "\n" +
		"POOL_MEMBER_ETH_ADDRESS=" + input.Enrollment.MemberEthAddress + "\n" +
		"POOL_ENROLLMENT_TOKEN_FILE=/workspace/enrollment-token\n" +
		"POOL_AGENT_CREDENTIALS_FILE=/workspace/agent-credentials.json\n" +
		"LIVEPEER_HOST_ID=" + input.Enrollment.ID + "\n" +
		"LIVEPEER_BROKER_URL=" + input.BrokerURL + "\n" +
		"LIVEPEER_BROKER_URLS=" + strings.Join(input.BrokerURLs, ",") + "\n" +
		"LIVEPEER_BROKER_QUIC_ADDR=" + input.BrokerQUICAddr + "\n" +
		"LIVEPEER_ATTACH_CREDENTIAL=" + input.Enrollment.BrokerSessionCredential + "\n" +
		// Public edge (plan 0046). Empty means this host is not public
		// and the pool places no session work on it. To become public:
		// set the https origin callers reach this host at, put tls.crt
		// and tls.key in ./edge, and open the port. The operator owns
		// DNS and certificate renewal (plan 0046 §7).
		"LIVEPEER_PUBLIC_URL=\n" +
		"LIVEPEER_EDGE_PORT=" + edgePort + "\n" +
		"LIVEPEER_EDGE_RTMPS_PORT=" + rtmpsPort + "\n"
}

func bundleReadme(input BundleInput) string {
	return "# Livepeer Pool member host\n\n" +
		"Run `sh start.sh` from this directory. It selects the host inventory runtime and starts the agent.\n\n" +
		"Keep each enrollment in its own directory. Selected-GPU bundles use dynamic\n" +
		"edge ports by default so another regional agent can stay running. Before\n" +
		"enabling a public URL, set unique fixed LIVEPEER_EDGE_PORT and\n" +
		"LIVEPEER_EDGE_RTMPS_PORT values for this enrollment.\n\n" +
		"The agent connects outbound to the Pool broker, reports the GPUs it can\n" +
		"see, and asks the Pool what it should be running. Broker-dispatched jobs\n" +
		"need only outbound connectivity. External sessions also need a public endpoint.\n\n" +
		"## Optional public session endpoint\n\n" +
		"The operator supplies DNS, a TLS certificate, and inbound connectivity.\n" +
		"The pool does not run DNS or issue certificates. Leave LIVEPEER_PUBLIC_URL\n" +
		"empty for an outbound-only host; it remains eligible for job workloads.\n\n" +
		"For sessions, point a hostname at this host, put the certificate chain in\n" +
		"./edge/tls.crt and its private key in ./edge/tls.key, and set\n" +
		"LIVEPEER_PUBLIC_URL=https://your-hostname:8443 in .env. Open/forward TCP\n" +
		"8443, or set LIVEPEER_EDGE_PORT=443 and omit :8443 from the URL. RTMPS\n" +
		"ingest additionally needs TCP 1936; keep its external port at 1936.\n" +
		"Run docker compose up -d --force-recreate pool_member_agent after changing\n" +
		"the environment. Renew certificates externally and restart the agent after\n" +
		"replacing them; it loads certificates at startup. Schedule restarts around\n" +
		"active sessions. The broker's session certification reach step must verify\n" +
		"the advertised endpoint. This does not solve CGNAT or WebRTC UDP routing.\n\n" +
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
		"To leave properly, retire the\n" +
		"host from the member portal first: your placements drain, in-flight\n" +
		"work finishes, and you stop being sent new jobs before the containers\n" +
		"go away. Then run `docker compose -f runners.compose.yaml down` and\n" +
		"`docker compose down` to stop the runner project and agent.\n\n" +
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
// Runners use a separate Compose project on the agent-owned network. The
// agent applies runners.compose.yaml; the bootstrap does not include it.
func bundleCompose(input BundleInput) string {
	image := input.MemberAgentImage
	if image == "" {
		image = "${REGISTRY:-tztcloud}/livepeer-pool-member-agent:${TAG:-v2.0.0}"
	}
	return "name: livepeer-agent-" + bundleNetworkID(input) + "\nservices:\n" +
		"  pool_member_agent:\n" +
		"    image: " + image + "\n" +
		"    restart: unless-stopped\n" +
		"    env_file: .env\n" +
		// The agent's edge (plan 0046 §2): the one TLS listener on the
		// host that callers of a session runner reach. Published even
		// on a host that never becomes public — the port is inert until
		// LIVEPEER_PUBLIC_URL is set and a certificate is mounted.
		"    ports:\n" +
		"      - \"${LIVEPEER_EDGE_PORT:-8443}:8443\"\n" +
		// RTMPS ingest for live session runners (plan 0046 §2.7): the
		// agent terminates TLS and forwards to the runner's rtmp_port.
		"      - \"${LIVEPEER_EDGE_RTMPS_PORT:-1936}:1936\"\n" +
		"    volumes:\n" +
		"      - ./edge:/etc/livepeer/edge:ro\n" +
		"      - ./pool-member-agent.yaml:/etc/livepeer/pool-member-agent.yaml:ro\n" +
		// The agent writes the runner compose file and drives docker,
		// so it needs the socket and a place to write. This is the
		// whole of what the pool asks of the host, and the member
		// README says so plainly rather than burying it.
		"      - /var/run/docker.sock:/var/run/docker.sock\n" +
		// Metadata-only host device view for inventory ownership/group facts.
		"      - /dev:/host-dev:ro\n" +
		// The agent initializes protected inode namespaces here and runner
		// compose fragments bind the same host path into every colocated
		// service. The identical source and target keep Docker-daemon path
		// resolution correct when the agent itself runs in a container.
		"      - /var/lib/livepeer-resource-admission:/var/lib/livepeer-resource-admission\n" +
		"      - ./:/workspace\n" +
		"    working_dir: /workspace\n" +
		"networks:\n  default:\n    name: livepeer-member-" + bundleNetworkID(input) + "\n"
}

func bundleUpdateScript() string {
	return "#!/usr/bin/env sh\n" +
		"set -eu\n" +
		": \"${POOL_CONTROLLER_URL:?POOL_CONTROLLER_URL is required}\"\n" +
		": \"${POOL_ENROLLMENT_ID:?POOL_ENROLLMENT_ID is required}\"\n" +
		"token=$(cat ./enrollment-token)\n" +
		"curl -fsS -H \"Authorization: Bearer ${token}\" \"${POOL_CONTROLLER_URL}/member/v1/enrollments/${POOL_ENROLLMENT_ID}/bundle\" -o bundle.zip\n" +
		"unzip -o bundle.zip\n" +
		"sh start.sh --pull always --force-recreate\n"
}

func bundleAgentConfig(input BundleInput) string {
	return "enrollment_id: " + input.Enrollment.ID + "\n" +
		"controller_url: " + input.ControllerURL + "\n" +
		"broker_url: " + input.BrokerURL + "\n" +
		"token_file: /workspace/enrollment-token\n"
}

// Shared with the member agent's desired-state renderer. Separate projects
// prevent runner --remove-orphans from deleting the agent; a shared network
// keeps runner service names reachable from the agent.
func bundleNetworkID(input BundleInput) string {
	sum := sha256.Sum256([]byte(input.Enrollment.ID))
	return hex.EncodeToString(sum[:8])
}

// Runtime selection is performed on the member host, before Docker starts the
// inventory agent. Intel/CPU hosts must not request an NVIDIA runtime.
func bundleStartScript() string {
	return `#!/bin/sh
set -eu
umask 077
nvidia=0
for device in /sys/bus/pci/devices/*; do
    [ -r "$device/vendor" ] && [ -r "$device/class" ] || continue
    [ "$(cat "$device/vendor")" = 0x10de ] || continue
    case "$(cat "$device/class")" in 0x03*) nvidia=1 ;; esac
done
if [ "$nvidia" = 1 ]; then
    command -v nvidia-smi >/dev/null || { echo 'NVIDIA GPU found; install its driver and Container Toolkit before starting.' >&2; exit 1; }
    nvidia-smi -L >/dev/null
fi
runtime_file=$(mktemp ./docker-compose.override.yaml.XXXXXX)
trap 'rm -f "$runtime_file"' EXIT HUP INT TERM
if [ "$nvidia" = 1 ]; then
    printf 'services:\n  pool_member_agent:\n    gpus: all\n' > "$runtime_file"
else
    printf 'services: {}\n' > "$runtime_file"
fi
mv "$runtime_file" docker-compose.override.yaml
docker compose up -d "$@" pool_member_agent
`
}
