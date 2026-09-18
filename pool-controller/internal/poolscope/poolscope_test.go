package poolscope

import (
	"strings"
	"testing"
)

func TestEnsureSupportedClaim(t *testing.T) {
	cases := []struct {
		name          string
		capabilityID  string
		protocol      string
		wantErr       bool
		wantSubstring string
	}{
		{
			name:         "supported openai chat",
			capabilityID: "openai:chat-completions",
			protocol:     "paid-job/v1",
			wantErr:      false,
		},
		{
			name:         "supported empty protocol",
			capabilityID: "rerank",
			protocol:     "",
			wantErr:      false,
		},
		{
			name:         "supported future paid-job major",
			capabilityID: "rerank",
			protocol:     "paid-job/v2",
			wantErr:      false,
		},
		{
			name:          "rejects video live rtmp capability",
			capabilityID:  CapabilityVideoLiveRTMP,
			protocol:      "paid-job/v1",
			wantErr:       true,
			wantSubstring: "video:live.rtmp",
		},
		{
			name:          "rejects paid-session protocol",
			capabilityID:  "video:transcode.abr",
			protocol:      "paid-session/v1",
			wantErr:       true,
			wantSubstring: "paid-session/v1",
		},
		{
			name:          "paid-session rejection cites plan 0032",
			capabilityID:  "livepeer:vtuber-session",
			protocol:      "paid-session/v1",
			wantErr:       true,
			wantSubstring: "0032-pool-live-rtmp-contract-decision",
		},
		{
			name:          "rejects malformed protocol tag",
			capabilityID:  "rerank",
			protocol:      "http-reqresp@v0",
			wantErr:       true,
			wantSubstring: "must match <name>/v<major>",
		},
		{
			name:          "rejects unknown protocol family",
			capabilityID:  "rerank",
			protocol:      "free-lunch/v1",
			wantErr:       true,
			wantSubstring: "paid-job/",
		},
		{
			name:          "rejects with surrounding whitespace",
			capabilityID:  "  " + CapabilityVideoLiveRTMP + " ",
			protocol:      "",
			wantErr:       true,
			wantSubstring: "video:live.rtmp",
		},
		{
			name:          "rejection cites plan 0032",
			capabilityID:  CapabilityVideoLiveRTMP,
			protocol:      "",
			wantErr:       true,
			wantSubstring: "0032-pool-live-rtmp-contract-decision",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureSupportedClaim(tc.capabilityID, tc.protocol)
			if tc.wantErr && err == nil {
				t.Fatalf("EnsureSupportedClaim(%q,%q) = nil, want error", tc.capabilityID, tc.protocol)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("EnsureSupportedClaim(%q,%q) = %v, want nil", tc.capabilityID, tc.protocol, err)
			}
			if tc.wantErr && tc.wantSubstring != "" && !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("EnsureSupportedClaim(%q,%q) error %q missing substring %q", tc.capabilityID, tc.protocol, err.Error(), tc.wantSubstring)
			}
		})
	}
}
