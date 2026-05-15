export interface RouteHealthPolicy {
    failureThreshold: number;
    cooldownMs: number;
}
export interface RouteOutcome {
    ok: boolean;
    retryable: boolean;
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
export declare class GenericRouteHealthTracker<TCandidate> {
    #private;
    constructor(input: GenericRouteHealthTrackerInput<TCandidate>);
    rankCandidates(candidates: TCandidate[], now?: number): TCandidate[];
    chooseRandom(candidates: TCandidate[], now?: number): TCandidate | null;
    record(candidate: TCandidate, outcome: RouteOutcome, reason?: string, now?: number): void;
    inspect(now?: number): RouteHealthSnapshot[];
    inspectMetrics(): RouteHealthMetrics;
    private isCoolingDown;
}
export declare function summarizeRouteHealth(health: Array<{
    coolingDown: boolean;
    consecutiveFailures: number;
    lastFailureAt: number | null;
    lastSuccessAt: number | null;
}>): RouteHealthSummary;
export declare function renderRouteHealthMetrics(gateway: string, summary: RouteHealthSummary, metrics: RouteHealthMetrics): string;
//# sourceMappingURL=index.d.ts.map