// Package version exports the spec-wide protocol version (plan 0043
// §3.7, decision 7).
//
// VERSION is the one string every component stamps and gates on:
//
//   - the broker writes it as spec_version on GET /registry/offerings
//     (protocols/broker-admin.md §7);
//   - the coordinator writes it as manifest.spec_version and refuses to
//     merge brokers whose major differs;
//   - the registry daemon validates manifests against the schema this
//     version describes.
//
// The VERSION file at the module root is the human-facing copy; the test
// in this package fails when the two drift, so a bump is always both.
package version

import (
	"fmt"
	"strconv"
	"strings"
)

// VERSION is the spec-wide SemVer from ../VERSION.
const VERSION = "2.4.1"

// Major returns the major component of VERSION.
func Major() int {
	m, _ := MajorOf(VERSION)
	return m
}

// MajorOf parses the major component of a spec_version string of the
// form major.minor or major.minor.patch (the manifest schema's pattern).
func MajorOf(v string) (int, error) {
	parts := strings.SplitN(strings.TrimSpace(v), ".", 3)
	if len(parts) < 2 {
		return 0, fmt.Errorf("spec_version %q: want major.minor[.patch]", v)
	}
	m, err := strconv.Atoi(parts[0])
	if err != nil || m < 0 {
		return 0, fmt.Errorf("spec_version %q: bad major", v)
	}
	return m, nil
}

// SameMajor reports whether v shares VERSION's major. Consumers gate on
// this and nothing finer: minors and patches are additive by definition
// (manifest/schema.json spec_version description).
func SameMajor(v string) bool {
	m, err := MajorOf(v)
	return err == nil && m == Major()
}
