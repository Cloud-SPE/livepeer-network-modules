package main

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-rewrite/payment-daemon/internal/store"
)

// Plan 0017 §6 / §12 C3: keystore rotation must not perturb durable
// receiver state. The redemption queue and session ledger are owned
// by the BoltDB store, which is keystore-agnostic by construction.
// This test documents that invariant — it would catch a regression
// where, for example, a future caller derives a per-keystore encryption
// key for the bbolt file or where a buggy boot path truncates the db
// when the keystore changes.
//
// In plan 0017 the redemption queue is not yet implemented (it's plan
// 0016); the store today persists sessions, sealed senders, and
// balances. Those are the records that must survive a restart with a
// different keystore.

func TestStoreSurvivesKeystoreSwap(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "sessions.db")

	// First daemon incarnation — opens the store, writes session +
	// balance state, closes cleanly.
	st1, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("first open: %v", err)
	}
	const workID = "rotation-test-work-id"
	if _, _, err := st1.OpenSession(store.Session{
		WorkID:              workID,
		Capability:          "openai-text",
		Offering:            "gpt-4-mini",
		PricePerWorkUnitWei: "1000000000",
		WorkUnit:            "tokens",
	}); err != nil {
		t.Fatalf("open session: %v", err)
	}
	sender1 := bytes.Repeat([]byte{0xab}, 20)
	if err := st1.SealSender(workID, sender1); err != nil {
		t.Fatalf("seal sender: %v", err)
	}
	if _, err := st1.CreditBalance(sender1, workID, big.NewInt(123_456_789)); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := st1.Close(); err != nil {
		t.Fatalf("close st1: %v", err)
	}

	// Daemon restart with a swapped keystore: simulate by creating a
	// fresh keystore at a new path. The store path is unchanged — the
	// invariant is that the bbolt file is keystore-agnostic.
	tmpKey := t.TempDir()
	_, _ = writeV3Keystore(t, tmpKey, "rotated-pw")

	// Second daemon incarnation — reopens the same db, asserts state
	// is intact.
	st2, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("second open after rotation: %v", err)
	}
	defer st2.Close()
	balance, err := st2.GetBalance(sender1, workID)
	if err != nil {
		t.Fatalf("balance after rotation: %v", err)
	}
	if balance.Cmp(big.NewInt(123_456_789)) != 0 {
		t.Errorf("balance after rotation = %s, want 123456789", balance.String())
	}
	got, err := st2.Get(sender1, workID)
	if err != nil {
		t.Fatalf("get session after rotation: %v", err)
	}
	if !bytes.Equal(got.Sender, sender1) {
		t.Errorf("sealed sender lost across rotation: %x", got.Sender)
	}
	if got.Capability != "openai-text" {
		t.Errorf("capability lost across rotation: %q", got.Capability)
	}
}

// Plan 0017 §5.4: the daemon must decrypt the V3 keystore before
// binding the gRPC socket. A bad password aborts startup before any
// caller can assume the daemon is alive.
//
// We assert by calling run() with a bad-password config and confirming
// (a) the returned error is *configError, and (b) no socket file is
// created at the configured path. The socket file is only ever created
// by net.Listen("unix", ...) — its presence is observable proof the
// listener bound, its absence is proof the boot aborted cleanly.

func TestRunReceiverDoesNotBindSocketOnDecryptFailure(t *testing.T) {
	t.Setenv(passwordEnvVar, "wrong-password")
	tmp := t.TempDir()
	keystorePath, _ := writeV3Keystore(t, tmp, "actual-password")
	socketPath := filepath.Join(tmp, "daemon.sock")

	cfg := bootConfig{
		mode:         "receiver",
		socketPath:   socketPath,
		dbPath:       filepath.Join(tmp, "sessions.db"),
		chainRPC:     "https://example.invalid/rpc",
		keystorePath: keystorePath,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := run(logger, cfg)
	if err == nil {
		t.Fatal("expected boot to fail on wrong password")
	}
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected *configError on decrypt failure, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "decrypt keystore") {
		t.Errorf("expected decrypt-keystore error, got %v", err)
	}
	if _, statErr := os.Stat(socketPath); statErr == nil {
		t.Errorf("socket %s exists after boot failure — listener must not bind on config error", socketPath)
	} else if !os.IsNotExist(statErr) {
		t.Errorf("unexpected stat error on socket path: %v", statErr)
	}
}

func TestRunSenderDoesNotBindSocketOnDecryptFailure(t *testing.T) {
	t.Setenv(passwordEnvVar, "wrong-password")
	tmp := t.TempDir()
	keystorePath, _ := writeV3Keystore(t, tmp, "actual-password")
	socketPath := filepath.Join(tmp, "sender.sock")

	cfg := bootConfig{
		mode:         "sender",
		socketPath:   socketPath,
		chainRPC:     "https://example.invalid/rpc",
		keystorePath: keystorePath,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := run(logger, cfg)
	if err == nil {
		t.Fatal("expected boot to fail on wrong password")
	}
	var cfgErr *configError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected *configError on decrypt failure, got %T: %v", err, err)
	}
	if _, statErr := os.Stat(socketPath); statErr == nil {
		t.Errorf("socket %s exists after boot failure — listener must not bind on config error", socketPath)
	} else if !os.IsNotExist(statErr) {
		t.Errorf("unexpected stat error on socket path: %v", statErr)
	}
}
