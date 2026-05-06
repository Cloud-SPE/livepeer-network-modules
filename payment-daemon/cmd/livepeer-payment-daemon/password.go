package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// passwordEnvVar is the environment variable consulted when
// --keystore-password-file is empty. Kept as a named constant so the
// startup log can reference it in error messages without drift.
const passwordEnvVar = "LIVEPEER_KEYSTORE_PASSWORD" //nolint:gosec // G101: env-var name, not a credential

// loadPassword returns the keystore unlock password from either the
// file at `filePath` or the LIVEPEER_KEYSTORE_PASSWORD env var. Both
// set is a misconfiguration (error); neither set is also an error.
// Trailing newlines in the file are trimmed so `echo hunter2 > pw` and
// `printf hunter2 > pw` are equivalent.
//
// Source: ported from
// `livepeer-modules-project/payment-daemon/cmd/livepeer-payment-daemon/password.go`
// at tag v4.1.3 (SHA caddeb342edb88faeea6a52e83a24c55704f0ef5). Per
// AGENTS.md lines 62-66 the port is a deliberate carryover; the source
// path and tag are recorded in the introducing commit.
//
// Plan 0017 §11.6 locked the scrubbing decision: simple `[]byte` zeroing
// post-decrypt, no third-party secure-memory dep. This function returns
// the password as a string (immutable in Go), but the *bytes* read from
// the password file are zeroed before return via zeroBytes() so they do
// not linger in the read buffer. The string copy lives in memory until
// the daemon's `loadKeystore` finishes calling DecryptKey; callers MUST
// drop the string reference promptly to let the GC reclaim it.
func loadPassword(filePath string) (string, error) {
	envPw := os.Getenv(passwordEnvVar)
	if filePath != "" && envPw != "" {
		return "", fmt.Errorf("--keystore-password-file and %s are mutually exclusive", passwordEnvVar)
	}
	if envPw != "" {
		return envPw, nil
	}
	if filePath == "" {
		return "", fmt.Errorf("keystore password required: set --keystore-password-file or %s", passwordEnvVar)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("read password file: %w", err)
	}
	// Defensive scrub: zero the file-read buffer once we've got the
	// trimmed password as a string. The string keeps an independent
	// allocation (Go strings are immutable / not aliased to []byte
	// slices), so the caller-visible password lives only in the
	// returned string.
	pw := strings.TrimRight(string(data), "\r\n")
	zeroBytes(data)
	if pw == "" {
		return "", errors.New("password file is empty")
	}
	return pw, nil
}

// zeroBytes overwrites every byte of b with zero. Used to scrub
// password material out of read buffers as soon as we no longer need
// them. Plan 0017 §11.6: deliberately minimal — we don't need
// memguard for a buffer that will be GC'd anyway, but zeroing makes
// process-memory inspection windows narrower.
func zeroBytes(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
