package types

import "testing"

func TestAuditKind_String(t *testing.T) {
	cases := []struct {
		in   AuditKind
		want string
	}{
		{AuditManifestFetched, "manifest_fetched"},
		{AuditManifestUnchanged, "manifest_unchanged"},
		{AuditManifestChanged, "manifest_changed"},
		{AuditSignatureInvalid, "signature_invalid"},
		{AuditChainURIChanged, "chain_uri_changed"},
		{AuditModeChanged, "mode_changed"},
		{AuditFallbackUsed, "fallback_used"},
		{AuditEvicted, "evicted"},
		{AuditPublishWritten, "publish_written"},
		{AuditPublishOnchain, "publish_onchain"},
		{AuditUnknown, "unknown"},
		{AuditKind(99), "unknown"},
	}
	for _, c := range cases {
		if got := c.in.String(); got != c.want {
			t.Fatalf("%v.String() = %q, want %q", c.in, got, c.want)
		}
	}
}
