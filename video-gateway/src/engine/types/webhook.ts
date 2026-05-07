export type WebhookEventType =
  | "video.upload.created"
  | "video.upload.completed"
  | "video.upload.expired"
  | "video.asset.created"
  | "video.asset.ready"
  | "video.asset.errored"
  | "video.asset.deleted"
  | "video.live_stream.created"
  | "video.live_stream.active"
  | "video.live_stream.reconnecting"
  | "video.live_stream.ended"
  | "video.live_stream.errored"
  | "video.live_stream.recording_ready"
  | "video.live_stream.runway_low"
  | "video.live_stream.topup_failed";

export interface WebhookEvent {
  type: WebhookEventType;
  data: Record<string, unknown>;
  occurredAt: Date;
}

export interface WebhookEndpoint {
  id: string;
  projectId: string;
  url: string;
  secret: string;
  eventTypes: WebhookEventType[] | null;
  createdAt: Date;
  disabledAt?: Date;
}

export interface WebhookDelivery {
  id: string;
  endpointId: string;
  eventType: WebhookEventType;
  status: "pending" | "delivered" | "failed";
  attempts: number;
  lastError?: string;
  createdAt: Date;
  deliveredAt?: Date;
}
