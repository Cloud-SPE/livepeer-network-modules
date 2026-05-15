import {
  GenericRouteHealthTracker,
  type RouteHealthMetrics,
  type RouteHealthSnapshot,
  type RouteOutcome,
} from "@livepeer-network-modules/gateway-route-health";

import type { OrchCandidate } from "./orchSelector.js";

export type { RouteHealthMetrics, RouteHealthSnapshot, RouteOutcome };

export class RouteHealthTracker extends GenericRouteHealthTracker<OrchCandidate> {
  constructor(input: { failureThreshold: number; cooldownMs: number }) {
    super({
      ...input,
      keyOf(candidate) {
        return [candidate.brokerUrl, candidate.ethAddress, candidate.capability, candidate.offering].join("|");
      },
    });
  }
}
