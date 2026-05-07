import type { WebhookEvent } from "../types/index.js";

export interface EventBus {
  emit(callerId: string, event: WebhookEvent): Promise<void>;
}
