package member

import (
	"strings"
	"testing"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func TestValidateJoinRequestRejectsOutOfPoolScopeClaim(t *testing.T) {
	cases := []struct {
		name            string
		capabilityID    string
		interactionMode string
		wantSubstring   string
	}{
		{
			name:            "video live rtmp capability",
			capabilityID:    "video:live.rtmp",
			interactionMode: "http-reqresp@v0",
			wantSubstring:   "video:live.rtmp",
		},
		{
			name:            "rtmp ingress hls egress interaction mode",
			capabilityID:    "openai:chat-completions",
			interactionMode: "rtmp-ingress-hls-egress@v0",
			wantSubstring:   "rtmp-ingress-hls-egress@v0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := types.JoinRequest{
				MemberEthAddress: "0xabc",
				PayoutMode:       "onchain",
				RequestedBackends: []types.RequestedBackend{{
					ID:        "backend-1",
					Transport: "http",
					URL:       "http://backend",
					ClaimedCapabilities: []types.ClaimedOffer{{
						CapabilityID:    tc.capabilityID,
						InteractionMode: tc.interactionMode,
					}},
				}},
			}
			err := validateJoinRequest(req)
			if err == nil {
				t.Fatalf("validateJoinRequest() expected rejection, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSubstring) {
				t.Fatalf("validateJoinRequest() err = %q, missing %q", err.Error(), tc.wantSubstring)
			}
			if !strings.Contains(err.Error(), "0032") {
				t.Fatalf("validateJoinRequest() err = %q, expected reference to plan 0032", err.Error())
			}
		})
	}
}

func TestValidateJoinRequestAllowsSupportedClaim(t *testing.T) {
	req := types.JoinRequest{
		MemberEthAddress: "0xabc",
		PayoutMode:       "onchain",
		RequestedBackends: []types.RequestedBackend{{
			ID:        "backend-1",
			Transport: "http",
			URL:       "http://backend",
			ClaimedCapabilities: []types.ClaimedOffer{{
				CapabilityID:    "openai:chat-completions",
				InteractionMode: "http-reqresp@v0",
			}},
		}},
	}
	if err := validateJoinRequest(req); err != nil {
		t.Fatalf("validateJoinRequest() unexpected error = %v", err)
	}
}
