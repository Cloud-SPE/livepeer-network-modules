// Package poolscope codifies the capability/protocol scope the current Pool
// implementation supports. Workloads outside that scope must be rejected at
// admission rather than silently skipped at probe time.
//
// The current scope rule comes from active plan 0032
// (docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md):
// the Pool member model is backend-runtime-only, while live / session
// workloads are broker-local (the broker stands the runner up itself and
// owns its session store, sealing key, and callback URL space). Until a
// remote-live member contract exists, those workloads cannot be served by a
// Pool backend.
package poolscope

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// CapabilityVideoLiveRTMP is the capability_id for the live RTMP
	// transcoding workload. Not supported by the current Pool member model.
	CapabilityVideoLiveRTMP = "video:live.rtmp"

	// ProtocolPaidJobPrefix is the only protocol family a Pool backend can
	// serve: a stateless priced request against a member-hosted runtime.
	ProtocolPaidJobPrefix = "paid-job/"

	// ProtocolPaidSessionPrefix is the long-lived session protocol. Serving
	// it requires broker-side session state (session_store, sealing key,
	// external_base_url) plus runner create/status/terminate paths, none of
	// which exist in the Pool member contract.
	ProtocolPaidSessionPrefix = "paid-session/"
)

// protocolRE mirrors the manifest schema's protocol pattern
// (livepeer-network-protocol/manifest/schema.json).
var protocolRE = regexp.MustCompile(`^[a-z][a-z0-9-]*/v[0-9]+$`)

// rejectionReason is the operator-visible explanation attached to every
// rejection raised by this package.
const rejectionReason = "pool member model is backend-runtime-only; " +
	"see docs/exec-plans/active/0032-pool-live-rtmp-contract-decision.md"

// EnsureSupportedClaim returns a non-nil error when the given
// capability_id / protocol combination is out of the current Pool scope.
// Either field may be empty: an empty value is considered "unspecified" and
// does not by itself trigger rejection.
func EnsureSupportedClaim(capabilityID, protocol string) error {
	capability := strings.TrimSpace(capabilityID)
	tag := strings.TrimSpace(protocol)
	if capability == CapabilityVideoLiveRTMP {
		return fmt.Errorf("capability_id %q is not supported by the pool: %s", capability, rejectionReason)
	}
	if tag == "" {
		return nil
	}
	if !protocolRE.MatchString(tag) {
		return fmt.Errorf("protocol %q must match <name>/v<major> (e.g. \"paid-job/v1\")", tag)
	}
	if strings.HasPrefix(tag, ProtocolPaidSessionPrefix) {
		return fmt.Errorf("protocol %q is not supported by the pool: %s", tag, rejectionReason)
	}
	if !strings.HasPrefix(tag, ProtocolPaidJobPrefix) {
		return fmt.Errorf("protocol %q is not supported by the pool: only %s* offerings can be served by a pool backend", tag, ProtocolPaidJobPrefix)
	}
	return nil
}
