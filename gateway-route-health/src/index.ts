export interface RouteHealthPolicy {
  failureThreshold: number;
  cooldownMs: number;
}

export interface RouteOutcome {
  ok: boolean;
  retryable: boolean;
}

interface RouteState {
  consecutiveFailures: number;
  cooldownUntil: number;
  lastFailureAt: number | null;
  lastFailureReason: string | null;
  lastSuccessAt: number | null;
}

export interface RouteHealthSnapshot {
  key: string;
  consecutiveFailures: number;
  coolingDown: boolean;
  cooldownUntil: number | null;
  lastFailureAt: number | null;
  lastFailureReason: string | null;
  lastSuccessAt: number | null;
}

export interface RouteHealthMetrics {
  attemptsTotal: number;
  successesTotal: number;
  retryableFailuresTotal: number;
  nonRetryableFailuresTotal: number;
  cooldownsOpenedTotal: number;
}

export interface RouteHealthSummary {
  tracked_routes: number;
  cooling_routes: number;
  routes_with_failures: number;
  latest_failure_at: number | null;
  latest_success_at: number | null;
}

export interface GenericRouteHealthTrackerInput<TCandidate> extends RouteHealthPolicy {
  keyOf(candidate: TCandidate): string;
}

export class GenericRouteHealthTracker<TCandidate> {
  readonly #failureThreshold: number;
  readonly #cooldownMs: number;
  readonly #keyOf: (candidate: TCandidate) => string;
  readonly #states = new Map<string, RouteState>();
  readonly #metrics: RouteHealthMetrics = {
    attemptsTotal: 0,
    successesTotal: 0,
    retryableFailuresTotal: 0,
    nonRetryableFailuresTotal: 0,
    cooldownsOpenedTotal: 0,
  };

  constructor(input: GenericRouteHealthTrackerInput<TCandidate>) {
    this.#failureThreshold = input.failureThreshold;
    this.#cooldownMs = input.cooldownMs;
    this.#keyOf = input.keyOf;
  }

  rankCandidates(candidates: TCandidate[], now = Date.now()): TCandidate[] {
    const ready: TCandidate[] = [];
    const cooling: TCandidate[] = [];
    for (const candidate of candidates) {
      if (this.isCoolingDown(candidate, now)) {
        cooling.push(candidate);
      } else {
        ready.push(candidate);
      }
    }
    return ready.length > 0 ? [...ready, ...cooling] : cooling;
  }

  chooseRandom(candidates: TCandidate[], now = Date.now()): TCandidate | null {
    if (candidates.length === 0) return null;
    const ready = candidates.filter((candidate) => !this.isCoolingDown(candidate, now));
    const pool = ready.length > 0 ? ready : candidates;
    return pool[Math.floor(Math.random() * pool.length)] ?? null;
  }

  record(candidate: TCandidate, outcome: RouteOutcome, reason?: string, now = Date.now()): void {
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

  inspect(now = Date.now()): RouteHealthSnapshot[] {
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

  inspectMetrics(): RouteHealthMetrics {
    return { ...this.#metrics };
  }

  private isCoolingDown(candidate: TCandidate, now: number): boolean {
    const state = this.#states.get(this.#keyOf(candidate));
    return (state?.cooldownUntil ?? 0) > now;
  }
}

export function summarizeRouteHealth(
  health: Array<{
    coolingDown: boolean;
    consecutiveFailures: number;
    lastFailureAt: number | null;
    lastSuccessAt: number | null;
  }>,
): RouteHealthSummary {
  let latestFailureAt: number | null = null;
  let latestSuccessAt: number | null = null;
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

export function renderRouteHealthMetrics(
  gateway: string,
  summary: RouteHealthSummary,
  metrics: RouteHealthMetrics,
): string {
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

function freshState(): RouteState {
  return {
    consecutiveFailures: 0,
    cooldownUntil: 0,
    lastFailureAt: null,
    lastFailureReason: null,
    lastSuccessAt: null,
  };
}
