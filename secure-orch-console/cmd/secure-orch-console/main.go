// Command secure-orch-console runs the cold-key host's diff-and-sign
// HTTP server. The server binds 127.0.0.1 only — never a routable
// interface. Operators reach it via `ssh -L` from a LAN laptop.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/audit"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/config"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/internal/signing"
	"github.com/Cloud-SPE/livepeer-network-rewrite/secure-orch-console/web"
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
		listen               = fs.String("listen", "127.0.0.1:8080", "Loopback bind address (must be 127.0.0.1, ::1, or localhost)")
		showVer              = fs.Bool("version", false, "Print version and exit")
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
		Keystore:       ks,
		LastSignedPath: *lastSignedPath,
		AuditLogPath:   *auditLogPath,
		Listen:         *listen,
	}
	if err := cfg.Validate(); err != nil {
		return err
	}

	signer, err := loadSigner(cfg.Keystore)
	if err != nil {
		return err
	}
	defer signer.Close()
	logger.Info("signer loaded", "address", signer.Address())

	auditLog, err := audit.Open(cfg.AuditLogPath)
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
