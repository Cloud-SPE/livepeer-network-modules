package config

import (
	"fmt"
	"strings"
)

// ParseRPCURLs parses the value of --chain-rpc-urls: a comma-separated
// list of JSON-RPC endpoints, primary first. Entries are trimmed. A blank
// entry ("https://a,,https://b") is an error rather than being dropped,
// because it is almost always a typo in an operator's env file and
// dropping it silently would change which endpoint is primary. An empty
// or all-blank value returns nil, nil so callers can decide whether
// "unset" is dev mode or a configuration error.
//
// This is the only parser for the flag; every daemon uses it so the
// validation and its error text are identical across the fleet.
func ParseRPCURLs(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, fmt.Errorf("--chain-rpc-urls: entry %d is empty", i+1)
		}
		out = append(out, p)
	}
	return out, nil
}
