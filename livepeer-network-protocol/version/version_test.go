package version

import (
	"os"
	"strings"
	"testing"
)

// The VERSION file and the constant must never drift: a bump is both or
// neither. This is the whole reason the package exists.
func TestConstantMatchesVersionFile(t *testing.T) {
	raw, err := os.ReadFile("../VERSION")
	if err != nil {
		t.Fatalf("read ../VERSION: %v", err)
	}
	if got := strings.TrimSpace(string(raw)); got != VERSION {
		t.Fatalf("VERSION file = %q, version.VERSION = %q — bump both", got, VERSION)
	}
}

func TestMajor(t *testing.T) {
	cases := map[string]struct {
		major int
		ok    bool
	}{
		"2.4.1": {2, true}, "2.4": {2, true}, "10.0.0": {10, true},
		"2": {0, false}, "x.1": {0, false}, "": {0, false}, "-1.0": {0, false},
	}
	for in, want := range cases {
		m, err := MajorOf(in)
		if (err == nil) != want.ok || m != want.major {
			t.Errorf("MajorOf(%q) = %d, %v; want %d, ok=%v", in, m, err, want.major, want.ok)
		}
	}
	if !SameMajor(VERSION) || SameMajor("99.0.0") || SameMajor("junk") {
		t.Fatal("SameMajor gate wrong")
	}
}
