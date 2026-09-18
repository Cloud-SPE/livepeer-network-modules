package main

import (
	"strings"
	"testing"
)

func TestPackageFloor(t *testing.T) {
	for _, tc := range []struct {
		name, profile string
		fail          bool
	}{
		{"exact floor", "mode: atomic\np/a.go:1.1,2.1 3 1\np/a.go:3.1,4.1 1 0\n", false},
		{"below floor", "mode: atomic\np/a.go:1.1,2.1 2 1\np/a.go:3.1,4.1 1 0\n", true},
		{"missing package", "mode: atomic\nother/a.go:1.1,2.1 100 1\n", true},
		{"bad header", "garbage", true},
		{"bad block", "mode: atomic\np/a.go 2 1\n", true},
		{"negative count", "mode: atomic\np/a.go:1.1,2.1 -1 1\n", true},
		{"duplicate block", "mode: atomic\np/a.go:1.1,2.1 1 1\np/a.go:1.1,2.1 1 1\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := check(strings.NewReader(tc.profile), map[string]bool{"p": true})
			if (err != nil) != tc.fail {
				t.Fatalf("got %v", err)
			}
		})
	}
}
