import type { WebhookEvent } from "../types/index.js";

export interface WebhookSink {
  enqueue(
    event: WebhookEvent,
    target: { url: string; secret: string },
  ): Promise<void>;
}
