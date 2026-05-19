package poolscope

import (
	"strings"
	"testing"
)

func TestEnsureSupportedClaim(t *testing.T) {
	cases := []struct {
		name            string
		capabilityID    string
		interactionMode string
		wantErr         bool
		wantSubstring   string
	}{
		{
			name:            "supported openai chat",
			capabilityID:    "openai:chat-completions",
			interactionMode: "http-reqresp@v0",
			wantErr:         false,
		},
		{
			name:            "supported empty interaction mode",
			capabilityID:    "rerank",
			interactionMode: "",
			wantErr:         false,
		},
		{
			name:            "rejects video live rtmp capability",
			capabilityID:    CapabilityVideoLiveRTMP,
			interactionMode: "http-reqresp@v0",
			wantErr:         true,
			wantSubstring:   "video:live.rtmp",
		},
		{
			name:            "rejects rtmp ingress hls egress interaction mode",
			capabilityID:    "video:transcode.abr",
			interactionMode: InteractionModeRTMPIngressHLSEgress,
			wantErr:         true,
			wantSubstring:   "rtmp-ingress-hls-egress@v0",
		},
		{
			name:            "rejects with surrounding whitespace",
			capabilityID:    "  " + CapabilityVideoLiveRTMP + " ",
			interactionMode: "",
			wantErr:         true,
			wantSubstring:   "video:live.rtmp",
		},
		{
			name:            "rejection cites plan 0032",
			capabilityID:    CapabilityVideoLiveRTMP,
			interactionMode: "",
			wantErr:         true,
			wantSubstring:   "0032-pool-live-rtmp-contract-decision",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := EnsureSupportedClaim(tc.capabilityID, tc.interactionMode)
			if tc.wantErr && err == nil {
				t.Fatalf("EnsureSupportedClaim(%q,%q) = nil, want error", tc.capabilityID, tc.interactionMode)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("EnsureSupportedClaim(%q,%q) = %v, want nil", tc.capabilityID, tc.interactionMode, err)
			}
			if tc.wantErr && tc.wantSubstring != "" && !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("EnsureSupportedClaim(%q,%q) error %q missing substring %q", tc.capabilityID, tc.interactionMode, err.Error(), tc.wantSubstring)
			}
		})
	}
}
