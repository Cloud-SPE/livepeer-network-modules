package canonical

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixturesRoot is the testdata path relative to this package's directory.
const fixturesRoot = "../../testdata/canonical"

// TestFixtures locks the canonical-bytes output for reference manifest
// inputs. The verifier (in livepeer-network-protocol/verify) and any
// future signer/recover toolchain MUST produce these exact bytes.
func TestFixtures(t *testing.T) {
	cases := []string{"manifest-minimal"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			input := mustRead(t, filepath.Join(fixturesRoot, name+".input.json"))
			want := mustRead(t, filepath.Join(fixturesRoot, name+".canonical.json"))
			got, err := BytesFromJSON(input)
			if err != nil {
				t.Fatalf("BytesFromJSON: %v", err)
			}
			// Strip trailing newline from the fixture file.
			wantStr := strings.TrimRight(string(want), "\n")
			if string(got) != wantStr {
				t.Fatalf("canonical mismatch\n want: %s\n got:  %s", wantStr, got)
			}
		})
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return b
}
