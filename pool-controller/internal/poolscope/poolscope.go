// Package poolscope codifies the capability/interaction-mode scope the
// current Pool implementation supports. Workloads outside that scope must
// be rejected at admission rather than silently skipped at probe time.
//
// The current scope rule comes from active plan 0032
// (docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md):
// the Pool member model is backend-runtime-only, while the shipped
// video:live.rtmp path is broker-local. Until a remote-live member
// contract exists, those workloads cannot be served by a Pool backend.
package poolscope

import (
	"fmt"
	"strings"
)

const (
	// CapabilityVideoLiveRTMP is the capability_id for the live RTMP
	// transcoding workload. Not supported by the current Pool member model.
	CapabilityVideoLiveRTMP = "video:live.rtmp"

	// InteractionModeRTMPIngressHLSEgress is the interaction_mode used by the
	// shipped broker-local live path. Not supported by the current Pool
	// member model.
	InteractionModeRTMPIngressHLSEgress = "rtmp-ingress-hls-egress@v0"
)

// rejectionReason is the operator-visible explanation attached to every
// rejection raised by this package.
const rejectionReason = "pool member model is backend-runtime-only; " +
	"see docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md"

// EnsureSupportedClaim returns a non-nil error when the given
// capability_id / interaction_mode combination is out of the current
// Pool scope. Either field may be empty: an empty value is considered
// "unspecified" and does not by itself trigger rejection.
func EnsureSupportedClaim(capabilityID, interactionMode string) error {
	cap := strings.TrimSpace(capabilityID)
	mode := strings.TrimSpace(interactionMode)
	if cap == CapabilityVideoLiveRTMP {
		return fmt.Errorf("capability_id %q is not supported by the pool: %s", cap, rejectionReason)
	}
	if mode == InteractionModeRTMPIngressHLSEgress {
		return fmt.Errorf("interaction_mode %q is not supported by the pool: %s", mode, rejectionReason)
	}
	return nil
}
