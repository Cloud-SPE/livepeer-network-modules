import type { Capability } from "../types/index.js";

export interface RateLimiter {
  consume(
    callerId: string,
    tier: string,
    capability: Capability,
  ): Promise<{ allowed: boolean; resetMs: number }>;
}
