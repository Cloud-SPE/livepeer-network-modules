export class GenericRouteHealthTracker {
    #failureThreshold;
    #cooldownMs;
    #keyOf;
    #states = new Map();
    #metrics = {
        attemptsTotal: 0,
        successesTotal: 0,
        retryableFailuresTotal: 0,
        nonRetryableFailuresTotal: 0,
        cooldownsOpenedTotal: 0,
    };
    constructor(input) {
        this.#failureThreshold = input.failureThreshold;
        this.#cooldownMs = input.cooldownMs;
        this.#keyOf = input.keyOf;
    }
    rankCandidates(candidates, now = Date.now()) {
        const ready = [];
        const cooling = [];
        for (const candidate of candidates) {
            if (this.isCoolingDown(candidate, now)) {
                cooling.push(candidate);
            }
            else {
                ready.push(candidate);
            }
        }
        return ready.length > 0 ? [...ready, ...cooling] : cooling;
    }
    chooseRandom(candidates, now = Date.now()) {
        if (candidates.length === 0)
            return null;
        const ready = candidates.filter((candidate) => !this.isCoolingDown(candidate, now));
        const pool = ready.length > 0 ? ready : candidates;
        return pool[Math.floor(Math.random() * pool.length)] ?? null;
    }
    record(candidate, outcome, reason, now = Date.now()) {
        const key = this.#keyOf(candidate);
        const state = this.#states.get(key) ?? freshState();
        this.#metrics.attemptsTotal += 1;
        if (outcome.ok) {
            this.#metrics.successesTotal += 1;
            state.consecutiveFailures = 0;
            state.cooldownUntil = 0;
            state.lastSuccessAt = now;
            this.#states.set(key, state);
            return;
        }
        if (!outcome.retryable) {
            this.#metrics.nonRetryableFailuresTotal += 1;
            this.#states.set(key, state);
            return;
        }
        this.#metrics.retryableFailuresTotal += 1;
        state.consecutiveFailures += 1;
        state.lastFailureAt = now;
        state.lastFailureReason = reason ?? null;
        if (state.consecutiveFailures >= this.#failureThreshold && state.cooldownUntil <= now) {
            this.#metrics.cooldownsOpenedTotal += 1;
            state.cooldownUntil = now + this.#cooldownMs;
        }
        this.#states.set(key, state);
    }
    inspect(now = Date.now()) {
        return [...this.#states.entries()].map(([key, state]) => ({
            key,
            consecutiveFailures: state.consecutiveFailures,
            coolingDown: state.cooldownUntil > now,
            cooldownUntil: state.cooldownUntil > 0 ? state.cooldownUntil : null,
            lastFailureAt: state.lastFailureAt,
            lastFailureReason: state.lastFailureReason,
            lastSuccessAt: state.lastSuccessAt,
        }));
    }
    inspectMetrics() {
        return { ...this.#metrics };
    }
    isCoolingDown(candidate, now) {
        const state = this.#states.get(this.#keyOf(candidate));
        return (state?.cooldownUntil ?? 0) > now;
    }
}
export function summarizeRouteHealth(health) {
    let latestFailureAt = null;
    let latestSuccessAt = null;
    for (const entry of health) {
        if (entry.lastFailureAt !== null && (latestFailureAt === null || entry.lastFailureAt > latestFailureAt)) {
            latestFailureAt = entry.lastFailureAt;
        }
        if (entry.lastSuccessAt !== null && (latestSuccessAt === null || entry.lastSuccessAt > latestSuccessAt)) {
            latestSuccessAt = entry.lastSuccessAt;
        }
    }
    return {
        tracked_routes: health.length,
        cooling_routes: health.filter((entry) => entry.coolingDown).length,
        routes_with_failures: health.filter((entry) => entry.consecutiveFailures > 0).length,
        latest_failure_at: latestFailureAt,
        latest_success_at: latestSuccessAt,
    };
}
export function renderRouteHealthMetrics(gateway, summary, metrics) {
    const label = `{gateway="${gateway}"}`;
    return [
        "# HELP livepeer_gateway_route_health_attempts_total Total Layer 3 route attempts observed by the gateway.",
        "# TYPE livepeer_gateway_route_health_attempts_total counter",
        `livepeer_gateway_route_health_attempts_total${label} ${metrics.attemptsTotal}`,
        "# HELP livepeer_gateway_route_health_successes_total Total successful Layer 3 route outcomes.",
        "# TYPE livepeer_gateway_route_health_successes_total counter",
        `livepeer_gateway_route_health_successes_total${label} ${metrics.successesTotal}`,
        "# HELP livepeer_gateway_route_health_retryable_failures_total Total retryable Layer 3 route failures.",
        "# TYPE livepeer_gateway_route_health_retryable_failures_total counter",
        `livepeer_gateway_route_health_retryable_failures_total${label} ${metrics.retryableFailuresTotal}`,
        "# HELP livepeer_gateway_route_health_non_retryable_failures_total Total non-retryable Layer 3 route failures.",
        "# TYPE livepeer_gateway_route_health_non_retryable_failures_total counter",
        `livepeer_gateway_route_health_non_retryable_failures_total${label} ${metrics.nonRetryableFailuresTotal}`,
        "# HELP livepeer_gateway_route_health_cooldowns_opened_total Total Layer 3 route cooldown windows opened.",
        "# TYPE livepeer_gateway_route_health_cooldowns_opened_total counter",
        `livepeer_gateway_route_health_cooldowns_opened_total${label} ${metrics.cooldownsOpenedTotal}`,
        "# HELP livepeer_gateway_route_health_tracked_routes Number of currently tracked Layer 3 routes.",
        "# TYPE livepeer_gateway_route_health_tracked_routes gauge",
        `livepeer_gateway_route_health_tracked_routes${label} ${summary.tracked_routes}`,
        "# HELP livepeer_gateway_route_health_cooling_routes Number of currently cooling Layer 3 routes.",
        "# TYPE livepeer_gateway_route_health_cooling_routes gauge",
        `livepeer_gateway_route_health_cooling_routes${label} ${summary.cooling_routes}`,
        "# HELP livepeer_gateway_route_health_routes_with_failures Number of tracked routes with recent failures.",
        "# TYPE livepeer_gateway_route_health_routes_with_failures gauge",
        `livepeer_gateway_route_health_routes_with_failures${label} ${summary.routes_with_failures}`,
        "",
    ].join("\n");
}
function freshState() {
    return {
        consecutiveFailures: 0,
        cooldownUntil: 0,
        lastFailureAt: null,
        lastFailureReason: null,
        lastSuccessAt: null,
    };
}
//# sourceMappingURL=index.js.map