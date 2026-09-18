package audioduration

import (
	"os"
	"path/filepath"
	"testing"
)

// Demonstrates that metering reads the ACTUAL uploaded bytes, not
// anything the backend says. The workload behind a transcription
// offering can be a stub and the billing is still real.
func TestMeteringReadsTheRealUpload(t *testing.T) {
	for _, tc := range []struct {
		file string
		want int64
	}{
		{"wav-16k-mono-3s.wav", 3},
		{"mp4-12.5s.m4a", 13},
		{"webm-9.5s.webm", 10},
	} {
		b, err := os.ReadFile(filepath.Join(fixtureDir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		got, err := EstimateCeilingSeconds(b)
		if err != nil {
			t.Fatalf("%s: %v", tc.file, err)
		}
		if got != tc.want {
			t.Fatalf("%s billed %ds; want %d", tc.file, got, tc.want)
		}
		t.Logf("%-24s -> %d billable seconds, read from the file itself", tc.file, got)
	}
}
