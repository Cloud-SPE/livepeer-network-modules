// Package jsonfile loads a decrypted ECDSA private key from a
// go-ethereum V3 JSON keystore file. It is a loader, not a
// providers.KeyStore implementation — the decrypted key is passed to
// keystore/inmemory.New for the providers.KeyStore role and (in
// plan 0016) to a transaction signer for `redeemWinningTicket`.
//
// Keeping this a loader avoids a second KeyStore implementation that
// would differ from inmemory only in how it sources its key.
//
// The V3 format is the standard produced by geth's `account new`, by
// MyCrypto/MEW exports, and by every other Ethereum wallet tool
// operators already use.
//
// Source: ported from
// `livepeer-modules-project/payment-daemon/internal/providers/keystore/jsonfile/jsonfile.go`
// at tag v4.1.3 (SHA caddeb342edb88faeea6a52e83a24c55704f0ef5). Per
// AGENTS.md lines 62-66 the port is a deliberate carryover; the source
// path and tag are recorded in the introducing commit.
package jsonfile

import (
	"crypto/ecdsa"
	"errors"
	"fmt"
	"os"

	ethkeystore "github.com/ethereum/go-ethereum/accounts/keystore"
)

// Load reads a V3 JSON keystore file at `path` and decrypts it with
// `password`. Returns the private key on success; the caller owns the
// key material and must not log or marshal it.
//
// Error text is operator-actionable per plan 0017 §5.2:
//
//   - empty path                 → "keystore path is required"
//   - empty password             → "keystore password is required"
//   - missing file               → wraps the underlying os error
//     ("read keystore: <path>: no such file …")
//   - empty file (zero bytes)    → "keystore file is empty"
//   - bad JSON / wrong password  → "decrypt keystore: <reason>"
//
// Trailing newline / `\r\n` trimming on the password is the caller's
// responsibility — see cmd/livepeer-payment-daemon/password.go.
func Load(path, password string) (*ecdsa.PrivateKey, error) {
	if path == "" {
		return nil, errors.New("jsonfile: keystore path is required")
	}
	if password == "" {
		return nil, errors.New("jsonfile: keystore password is required")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read keystore: %w", err)
	}
	if len(data) == 0 {
		return nil, errors.New("keystore file is empty")
	}
	key, err := ethkeystore.DecryptKey(data, password)
	if err != nil {
		return nil, fmt.Errorf("decrypt keystore: %w", err)
	}
	return key.PrivateKey, nil
}
