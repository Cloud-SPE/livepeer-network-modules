// Thin wrapper around the shared ApiClient that adds the daydream
// portal endpoints. Keeping it here (vs reaching for the shared
// client directly in components) means a future API rename only has
// to touch this file.

import { ApiClient } from "@livepeer-network-modules/customer-portal-shared";

export interface OpenSessionResponse {
  session_id: string;
  scope_url: string;
  orchestrator: string | null;
}

export interface UsageSummary {
  window_days: number;
  total_sessions: number;
  total_seconds: number;
}

export interface UsageEvent {
  id: string;
  session_id: string;
  orchestrator: string | null;
  started_at: string;
  ended_at: string | null;
  duration_seconds: number | null;
}

export interface SavedPrompt {
  id: string;
  label: string;
  body: string;
  created_at: string;
  updated_at: string;
}

export interface WaitlistStatus {
  status: "unknown" | "pending" | "approved" | "rejected";
}

export class DaydreamPortalApi {
  private readonly client: ApiClient;

  constructor(baseUrl: string = "") {
    this.client = new ApiClient({ baseUrl });
  }

  signupWaitlist(input: {
    email: string;
    display_name?: string;
    reason?: string;
  }): Promise<{ waitlist_id: string; status: string }> {
    return this.client.post("/portal/waitlist", input);
  }

  waitlistStatus(email: string): Promise<WaitlistStatus> {
    return this.client.get(
      `/portal/waitlist/status?email=${encodeURIComponent(email)}`,
    );
  }

  openSession(params?: Record<string, unknown>): Promise<OpenSessionResponse> {
    return this.client.post("/portal/sessions", { params: params ?? {} });
  }

  closeSession(sessionId: string): Promise<{
    session_id: string;
    closed: boolean;
    duration_seconds: number | null;
  }> {
    return this.client.post(
      `/portal/sessions/${encodeURIComponent(sessionId)}/close`,
    );
  }

  listPrompts(): Promise<{ prompts: SavedPrompt[] }> {
    return this.client.get("/portal/prompts");
  }

  createPrompt(input: { label: string; body: string }): Promise<{ id: string }> {
    return this.client.post("/portal/prompts", input);
  }

  updatePrompt(
    id: string,
    input: { label?: string; body?: string },
  ): Promise<{ id: string }> {
    return this.client.request("PATCH", `/portal/prompts/${id}`, input);
  }

  deletePrompt(id: string): Promise<void> {
    return this.client.request("DELETE", `/portal/prompts/${id}`);
  }

  usageSummary(): Promise<UsageSummary> {
    return this.client.get("/portal/usage/summary");
  }

  usageRecent(): Promise<{ events: UsageEvent[] }> {
    return this.client.get("/portal/usage/recent");
  }
}
