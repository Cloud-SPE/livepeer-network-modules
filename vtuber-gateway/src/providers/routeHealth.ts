import {
  GenericRouteHealthTracker,
  type RouteHealthMetrics,
  type RouteHealthSnapshot,
  type RouteOutcome,
} from "@livepeer-network-modules/gateway-route-health";

import type { NodeDescriptor } from "./serviceRegistry.js";

export type { RouteHealthMetrics, RouteHealthSnapshot, RouteOutcome };

export class RouteHealthTracker extends GenericRouteHealthTracker<NodeDescriptor> {
  constructor(input: { failureThreshold: number; cooldownMs: number }) {
    super({
      ...input,
      keyOf(candidate) {
        return [candidate.nodeUrl, candidate.ethAddress, candidate.capabilities.join(","), candidate.offering ?? ""].join("|");
      },
    });
  }
}
