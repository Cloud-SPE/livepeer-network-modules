// Command secure-orch-console runs the cold-key host's diff-and-sign
// HTTP server.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/agent"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/policy"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/internal/signing"
	"github.com/Cloud-SPE/livepeer-network-modules/secure-orch-console/web"
)

var version = "dev"

const configErrExitCode = 2

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(configErrExitCode)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("secure-orch-console", flag.ContinueOnError)
	var (
		keystoreFlag         = fs.String("keystore", "", "Keystore selector: v3:<path>")
		keystorePasswordFile = fs.String("keystore-password-file", "", "File containing the V3 keystore password (or LIVEPEER_KEYSTORE_PASSWORD env)")
		lastSignedPath       = fs.String("last-signed", "/var/lib/secure-orch/last-signed.json", "Path to the canonical last-signed envelope used by the diff renderer")
		auditLogPath         = fs.String("audit-log", "/var/log/secure-orch/audit.log.jsonl", "Append-only JSONL audit log")
		auditRotateSize      = fs.Int64("audit-rotate-size", audit.DefaultRotateSize, "Audit log size threshold for rotation, in bytes (0 disables)")
		listen               = fs.String("listen", "127.0.0.1:8080", "HTTP listen address (explicit host:port required)")
		coordinatorURL       = fs.String("coordinator-url", "", "Optional orch-coordinator base URL used for operator cross-links (required with --agent: candidate pull + signed-manifest push)")
		showVer              = fs.Bool("version", false, "Print version and exit")

		agentMode            = fs.Bool("agent", false, "Run the plan 0042 sign-cycle agent loop alongside the console")
		coordinatorPublicURL = fs.String("coordinator-public-url", "", "orch-coordinator public base URL for publish confirmation (required with --agent)")
		coordinatorTokenFile = fs.String("coordinator-token-file", "", "File holding the agent bearer token for the coordinator (required with --agent)")
		agentPolicyPath      = fs.String("agent-policy", "/etc/secure-orch/sign-policy.json", "Sign-policy file (strict JSON; schema in docs/sign-policy.schema.json)")
		agentHeldDir         = fs.String("agent-held-dir", "/var/lib/secure-orch/held", "Directory for the held-for-operator candidate slot")
		agentPauseFile       = fs.String("agent-pause-file", "/var/lib/secure-orch/agent.pause", "Kill switch: this file's presence pauses agent pull and sign")
		agentPollInterval    = fs.Duration("agent-poll-interval", 60*time.Second, "Agent conditional-GET poll cadence (±10% jitter)")
		alertWebhookURL      = fs.String("alert-webhook-url", "", "Optional outbound webhook for agent alerts (held, forbidden, publish/policy failure, rate-limit pause, expiry warning); best-effort, audit log is the system of record")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVer {
		fmt.Println(version)
		return nil
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ks, err := config.ParseKeystore(*keystoreFlag, *keystorePasswordFile)
	if err != nil {
		return err
	}
	cfg := config.Config{
		Keystore:        ks,
		LastSignedPath:  *lastSignedPath,
		AuditLogPath:    *auditLogPath,
		AuditRotateSize: *auditRotateSize,
		Listen:          *listen,
		ProtocolSocket:  strings.TrimSpace(os.Getenv("PROTOCOL_DAEMON_SOCKET")),
		AdminTokens:     parseCSVEnv("SECURE_ORCH_ADMIN_TOKENS"),
		CoordinatorURL:  *coordinatorURL,
		Version:         version,
		AgentHeldDir:    *agentHeldDir,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	cfg.CoordinatorURL, err = config.NormalizeBaseURL(cfg.CoordinatorURL)
	if err != nil {
		return err
	}

	signer, err := loadSigner(cfg.Keystore)
	if err != nil {
		return err
	}
	defer signer.Close()
	logger.Info("signer loaded", "address", signer.Address())

	auditLog, err := audit.Open(cfg.AuditLogPath, cfg.AuditRotateSize)
	if err != nil {
		return err
	}
	defer auditLog.Close()

	if err := auditLog.Append(audit.Event{
		Kind:       audit.KindBoot,
		EthAddress: signer.Address().String(),
		Note:       "secure-orch-console " + version,
	}); err != nil {
		logger.Warn("audit boot append failed", "err", err)
	}

	srv, err := web.New(cfg, signer, auditLog, logger)
	if err != nil {
		return err
	}
	addr, err := srv.Listen()
	if err != nil {
		return err
	}
	logger.Info("listening", "addr", addr)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if *agentMode {
		ag, err := buildAgent(agentBoot{
			coordinatorURL:       cfg.CoordinatorURL,
			coordinatorPublicURL: *coordinatorPublicURL,
			tokenFile:            *coordinatorTokenFile,
			policyPath:           *agentPolicyPath,
			lastSignedPath:       cfg.LastSignedPath,
			heldDir:              *agentHeldDir,
			pauseFile:            *agentPauseFile,
			pollInterval:         *agentPollInterval,
			alertWebhookURL:      *alertWebhookURL,
		}, signer, auditLog, logger.With("component", "agent"))
		if err != nil {
			return err
		}
		srv.SetMetricsHandler(ag.Metrics().Handler())
		go ag.Run(ctx)
		logger.Info("agent loop started", "coordinator", cfg.CoordinatorURL, "policy", *agentPolicyPath)
	}

	serveErr := srv.Serve(ctx)

	if err := auditLog.Append(audit.Event{
		Kind:       audit.KindShutdown,
		EthAddress: signer.Address().String(),
	}); err != nil {
		logger.Warn("audit shutdown append failed", "err", err)
	}
	if serveErr != nil {
		return serveErr
	}
	return nil
}

type agentBoot struct {
	coordinatorURL       string
	coordinatorPublicURL string
	tokenFile            string
	policyPath           string
	lastSignedPath       string
	heldDir              string
	pauseFile            string
	pollInterval         time.Duration
	alertWebhookURL      string
}

func buildAgent(b agentBoot, signer signing.Signer, auditLog *audit.Log, logger *slog.Logger) (*agent.Agent, error) {
	if b.coordinatorURL == "" {
		return nil, errors.New("--agent requires --coordinator-url")
	}
	if b.coordinatorPublicURL == "" {
		return nil, errors.New("--agent requires --coordinator-public-url")
	}
	publicURL, err := config.NormalizeBaseURL(b.coordinatorPublicURL)
	if err != nil {
		return nil, fmt.Errorf("--coordinator-public-url: %w", err)
	}
	if b.tokenFile == "" {
		return nil, errors.New("--agent requires --coordinator-token-file")
	}
	tokenBytes, err := os.ReadFile(b.tokenFile) //nolint:gosec // operator-supplied
	if err != nil {
		return nil, fmt.Errorf("--coordinator-token-file: %w", err)
	}
	token := strings.TrimSpace(string(tokenBytes))
	if token == "" {
		return nil, fmt.Errorf("--coordinator-token-file: %s is empty", b.tokenFile)
	}
	// The policy must load at boot — starting the agent with a bad
	// policy is a config error, not a runtime pause.
	if _, err := policy.Load(b.policyPath); err != nil {
		return nil, err
	}
	client := &agent.Client{
		AdminURL:  strings.TrimRight(b.coordinatorURL, "/"),
		PublicURL: strings.TrimRight(publicURL, "/"),
		Token:     token,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
	return agent.New(agent.Config{
		PolicyPath:     b.policyPath,
		LastSignedPath: b.lastSignedPath,
		HeldDir:        b.heldDir,
		PauseFile:      b.pauseFile,
		PollInterval:   b.pollInterval,
	}, client, signer, auditLog, logger, agent.NewWebhookAlert(b.alertWebhookURL, logger)), nil
}

func loadSigner(ks config.Keystore) (*signing.Keystore, error) {
	password, err := readPassword(ks.PasswordFile)
	if err != nil {
		return nil, err
	}
	return signing.LoadKeystore(ks.Path, password)
}

func readPassword(path string) (string, error) {
	if env := os.Getenv("LIVEPEER_KEYSTORE_PASSWORD"); env != "" {
		if path != "" {
			return "", errors.New("LIVEPEER_KEYSTORE_PASSWORD and --keystore-password-file are mutually exclusive")
		}
		return env, nil
	}
	if path == "" {
		return "", errors.New("keystore password required: set LIVEPEER_KEYSTORE_PASSWORD or pass --keystore-password-file")
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is operator-supplied
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func parseCSVEnv(name string) []string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if !slices.Contains(out, part) {
			out = append(out, part)
		}
	}
	return out
}
