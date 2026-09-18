package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/Cloud-SPE/livepeer-network-modules/capability-broker/internal/settlement"
)

const settlementKeyUsage = `usage:
  livepeer-capability-broker settlement-key generate --out <file>
      Create a new delegated settlement signing key (hex secp256k1, mode 0600)
      and print its public key. Refuses to overwrite.
  livepeer-capability-broker settlement-key pubkey --file <file>
      Print the public key for an existing key file — the value the
      coordinator delegates in the manifest's settlement_keys block.
`

// runSettlementKey is the `settlement-key` subcommand: the operator's
// way to mint a delegated key and to read its public half without
// fishing it out of a startup log line.
func runSettlementKey(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, settlementKeyUsage)
		return 2
	}
	switch args[0] {
	case "generate":
		fs := flag.NewFlagSet("settlement-key generate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		out := fs.String("out", "", "path to write the private key to (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *out == "" {
			fmt.Fprintln(stderr, "settlement-key generate: --out is required")
			return 2
		}
		pub, err := generateSettlementKey(*out)
		if err != nil {
			fmt.Fprintf(stderr, "settlement-key generate: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, pub)
		return 0
	case "pubkey":
		fs := flag.NewFlagSet("settlement-key pubkey", flag.ContinueOnError)
		fs.SetOutput(stderr)
		file := fs.String("file", "", "path of the private key file (required)")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *file == "" {
			fmt.Fprintln(stderr, "settlement-key pubkey: --file is required")
			return 2
		}
		signer, err := settlement.LoadSigner(*file)
		if err != nil {
			fmt.Fprintf(stderr, "settlement-key pubkey: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, signer.PublicKeyHex())
		return 0
	default:
		fmt.Fprintf(stderr, "settlement-key: unknown subcommand %q\n%s", args[0], settlementKeyUsage)
		return 2
	}
}

// generateSettlementKey writes a fresh key and returns its public half.
// O_EXCL: a key file is never silently replaced, because the manifest
// that delegates the old one is still out there.
func generateSettlementKey(path string) (string, error) {
	key, err := crypto.GenerateKey()
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("%s exists; refusing to overwrite a settlement key", path)
		}
		return "", err
	}
	if _, err := fmt.Fprintln(f, hex.EncodeToString(crypto.FromECDSA(key))); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return "0x" + hex.EncodeToString(crypto.FromECDSAPub(&key.PublicKey)), nil
}
