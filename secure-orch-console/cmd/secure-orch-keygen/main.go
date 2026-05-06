// Command secure-orch-keygen mints a fresh secp256k1 cold key on the
// secure-orch host and writes it as a V3 JSON keystore. The eth
// address is printed to stdout for the operator to authorize on chain
// (BondingManager.setSigningAddress or its protocol equivalent —
// plan 0019 §10).
//
// The key is generated with go-ethereum's crypto.GenerateKey (which
// reads from crypto/rand) and never leaves this process. The
// keystore file is written 0600 to a path the operator chooses; the
// password is read from a file to avoid TTY-echo footguns.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/keystore"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("secure-orch-keygen", flag.ContinueOnError)
	var (
		out          = fs.String("out", "", "Path to write the V3 JSON keystore (0600). Required.")
		passwordFile = fs.String("password-file", "", "Path to a file containing the keystore password. Required.")
		force        = fs.Bool("force", false, "Overwrite an existing keystore file")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *out == "" {
		return errors.New("--out is required")
	}
	if *passwordFile == "" {
		return errors.New("--password-file is required")
	}
	if _, err := os.Stat(*out); err == nil && !*force {
		return fmt.Errorf("%s exists; pass --force to overwrite", *out)
	}
	pwBytes, err := os.ReadFile(*passwordFile) //nolint:gosec // operator-supplied
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	password := strings.TrimRight(string(pwBytes), "\r\n")
	if len(password) < 12 {
		return errors.New("password must be at least 12 characters")
	}

	priv, err := crypto.GenerateKey()
	if err != nil {
		return fmt.Errorf("generate key: %w", err)
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	key := &keystore.Key{
		Id:         id,
		Address:    crypto.PubkeyToAddress(priv.PublicKey),
		PrivateKey: priv,
	}
	encrypted, err := keystore.EncryptKey(key, password, keystore.StandardScryptN, keystore.StandardScryptP)
	if err != nil {
		return fmt.Errorf("encrypt: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*out), 0o700); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(*out, encrypted, 0o600); err != nil {
		return fmt.Errorf("write keystore: %w", err)
	}
	fmt.Fprintf(stdout, "wrote %s\naddress: %s\n", *out, strings.ToLower(key.Address.Hex()))
	return nil
}
