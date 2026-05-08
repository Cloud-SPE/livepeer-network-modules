package types

import (
	"errors"
	"testing"
)

func TestManifestValidationError_FormatsAndUnwraps(t *testing.T) {
	e := NewValidation(ErrParse, "nodes[2].url", "must be https")
	if e.Error() != "parse_error at nodes[2].url: must be https" {
		t.Fatalf("unexpected error format: %s", e.Error())
	}
	if !errors.Is(e, ErrParse) {
		t.Fatalf("expected unwrap to ErrParse, got %v", errors.Unwrap(e))
	}
}

func TestManifestValidationError_Variants(t *testing.T) {
	cases := []struct {
		e    *ManifestValidationError
		want string
	}{
		{NewValidation(ErrParse, "", ""), "parse_error"},
		{NewValidation(ErrParse, "nodes[0]", ""), "parse_error at nodes[0]"},
		{NewValidation(ErrParse, "", "hint only"), "parse_error: hint only"},
	}
	for _, c := range cases {
		if c.e.Error() != c.want {
			t.Fatalf("got %q, want %q", c.e.Error(), c.want)
		}
	}
}
