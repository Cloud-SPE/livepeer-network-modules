// Package metrics owns the Prometheus listener and the protocol-daemon's
// metric naming constants.
//
// chain-commons does not import prometheus/client_golang. This package is
// the only place in protocol-daemon that does. The Recorder interface
// elsewhere goes through chain-commons.providers.metrics so the rest of
// the daemon doesn't need a prometheus dep.
package metrics

// Metric names — all prefixed with livepeer_protocol_*. See
// docs/conventions/metrics.md for the rules.
const (
	NameRoundInitTotal           = "livepeer_protocol_round_init_total"
	NameRoundInitDurationSeconds = "livepeer_protocol_round_init_duration_seconds"

	NameRewardTotal           = "livepeer_protocol_reward_total"
	NameRewardEarnedWeiTotal  = "livepeer_protocol_reward_earned_wei_total"
	NameRewardDurationSeconds = "livepeer_protocol_reward_duration_seconds"
	NameEligibleRoundCount    = "livepeer_protocol_eligible_round_count"
	NameActiveStatus          = "livepeer_protocol_active_status"

	NameBuildInfo     = "livepeer_protocol_build_info"
	NameUptimeSeconds = "livepeer_protocol_uptime_seconds"
)
