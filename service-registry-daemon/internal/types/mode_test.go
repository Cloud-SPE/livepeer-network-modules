package types

import "testing"

func TestResolveMode_String(t *testing.T) {
	cases := []struct {
		in   ResolveMode
		want string
	}{
		{ModeWellKnown, "well-known"},
		{ModeCSV, "csv"},
		{ModeLegacy, "legacy"},
		{ModeUnknown, "unknown"},
		{ResolveMode(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSignatureStatus_String(t *testing.T) {
	cases := []struct {
		in   SignatureStatus
		want string
	}{
		{SigVerified, "signed-verified"},
		{SigUnsigned, "unsigned"},
		{SigLegacy, "legacy"},
		{SigInvalid, "signature-invalid"},
		{SigUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestFreshnessStatus_String(t *testing.T) {
	cases := []struct {
		in   FreshnessStatus
		want string
	}{
		{Fresh, "fresh"},
		{StaleRecoverable, "stale_recoverable"},
		{StaleFailing, "stale_failing"},
		{FreshUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSource_String(t *testing.T) {
	cases := []struct {
		in   Source
		want string
	}{
		{SourceManifest, "manifest"},
		{SourceLegacy, "legacy"},
		{SourceStaticOverlay, "static-overlay"},
		{SourceCSVFallback, "csv-fallback"},
		{SourceUnknown, "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}
