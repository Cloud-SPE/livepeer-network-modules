package sessionstore

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
)

// LoadKeyFile reads the sealing key named by config
// session_store.sealing_key_file. The file holds either the raw 32
// bytes or their 64-char hex encoding (surrounding whitespace
// tolerated). Anything else is an error — a wrong-size key must fail
// startup, never silently truncate.
func LoadKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("sessionstore: read key file: %w", err)
	}
	if len(raw) == KeySize {
		return raw, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == KeySize {
		return trimmed, nil
	}
	if len(trimmed) == KeySize*2 {
		key := make([]byte, KeySize)
		if _, err := hex.Decode(key, trimmed); err == nil {
			return key, nil
		}
	}
	return nil, fmt.Errorf("sessionstore: key file %s must hold %d raw bytes or %d hex chars (got %d bytes)",
		path, KeySize, KeySize*2, len(raw))
}
