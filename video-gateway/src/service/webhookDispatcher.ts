import type { WebhookEvent } from "../engine/types/index.js";
import { signEvent } from "../engine/service/webhookSigner.js";

export interface DispatchInput {
  endpointUrl: string;
  endpointSecret: string;
  event: WebhookEvent;
  deliveryId: string;
  pepper: string;
}

export interface DispatchResult {
  status: number;
  ok: boolean;
  body?: string;
}

export async function dispatchWebhook(input: DispatchInput): Promise<DispatchResult> {
  const body = JSON.stringify({
    type: input.event.type,
    occurred_at: input.event.occurredAt.toISOString(),
    data: input.event.data,
  });
  const signed = signEvent({
    secret: input.endpointSecret + input.pepper,
    body,
    eventType: input.event.type,
    deliveryId: input.deliveryId,
  });
  const headers: Record<string, string> = { ...signed };

  const res = await fetch(input.endpointUrl, {
    method: "POST",
    body,
    headers,
  });
  const result: DispatchResult = { status: res.status, ok: res.ok };
  try {
    result.body = await res.text();
  } catch {
    /* non-text body; skip */
  }
  return result;
}
