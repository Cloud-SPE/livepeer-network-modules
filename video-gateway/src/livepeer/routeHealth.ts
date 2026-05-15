import {
  GenericRouteHealthTracker,
  type RouteHealthMetrics,
  type RouteHealthPolicy,
  type RouteHealthSnapshot,
  type RouteOutcome,
} from "@livepeer-network-modules/gateway-route-health";

import type { VideoRouteCandidate } from "./routeSelector.js";

export type { RouteHealthMetrics, RouteHealthPolicy, RouteHealthSnapshot, RouteOutcome };

export class RouteHealthTracker extends GenericRouteHealthTracker<VideoRouteCandidate> {
  constructor(policy: RouteHealthPolicy) {
    super({
      ...policy,
      keyOf(candidate) {
        return [candidate.brokerUrl, candidate.ethAddress, candidate.capability, candidate.offering].join("|");
      },
    });
  }
}
