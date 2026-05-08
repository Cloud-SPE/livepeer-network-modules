package main

import (
	"os"
	"strings"
)

// readPassword resolves the keystore password from (in priority order):
//  1. --keystore-password-file
//  2. LIVEPEER_KEYSTORE_PASSWORD env
//  3. empty string
func readPassword(file string) string {
	if file != "" {
		raw, err := os.ReadFile(file) //nolint:gosec // operator-supplied path
		if err == nil {
			return strings.TrimRight(string(raw), "\r\n")
		}
	}
	return os.Getenv("LIVEPEER_KEYSTORE_PASSWORD")
}
