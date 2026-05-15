// Daydream-portal user API. Bearer the UI token on every authed call;
// public endpoints (waitlist, login-by-key, status) don't send auth.

import { readSession } from "./session";

export class ApiError extends Error {
  constructor(public status: number, public body: unknown) {
    super(
      typeof body === "string"
        ? body
        : (body as { message?: string })?.message ?? `HTTP ${status}`,
    );
  }
}

async function request<T>(
  method: string,
  path: string,
  body?: unknown,
  { auth = true }: { auth?: boolean } = {},
): Promise<T> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["content-type"] = "application/json";
  if (auth) {
    const s = readSession();
    if (s) headers["authorization"] = `Bearer ${s.token}`;
  }
  const res = await fetch(path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "same-origin",
  });
  const text = await res.text();
  let parsed: unknown;
  try {
    parsed = text ? JSON.parse(text) : undefined;
  } catch {
    parsed = text;
  }
  if (!res.ok) throw new ApiError(res.status, parsed);
  return parsed as T;
}

export interface WaitlistStatus {
  status: "unknown" | "pending" | "approved" | "rejected";
}
export interface SavedPrompt {
  id: string;
  label: string;
  body: string;
  created_at: string;
  updated_at: string;
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
export interface OpenSessionResponse {
  session_id: string;
  scope_url: string;
  orchestrator: string | null;
}
export interface LoginByKeyResponse {
  auth_token: string;
  auth_token_id: string;
  customer: { id: string; email: string };
}

export const api = {
  signupWaitlist: (input: { email: string; display_name?: string; reason?: string }) =>
    request<{ waitlist_id: string; status: string }>("POST", "/portal/waitlist", input, { auth: false }),
  waitlistStatus: (email: string) =>
    request<WaitlistStatus>(
      "GET",
      `/portal/waitlist/status?email=${encodeURIComponent(email)}`,
      undefined,
      { auth: false },
    ),
  loginByKey: (input: { api_key: string; actor: string }) =>
    request<LoginByKeyResponse>("POST", "/portal/login-by-key", input, { auth: false }),

  openSession: (params?: Record<string, unknown>) =>
    request<OpenSessionResponse>("POST", "/portal/sessions", { params: params ?? {} }),
  closeSession: (id: string) =>
    request<{ session_id: string; closed: boolean; duration_seconds: number | null }>(
      "POST",
      `/portal/sessions/${encodeURIComponent(id)}/close`,
    ),
  listPrompts: () => request<{ prompts: SavedPrompt[] }>("GET", "/portal/prompts"),
  createPrompt: (input: { label: string; body: string }) =>
    request<{ id: string }>("POST", "/portal/prompts", input),
  updatePrompt: (id: string, input: { label?: string; body?: string }) =>
    request<{ id: string }>("PATCH", `/portal/prompts/${id}`, input),
  deletePrompt: (id: string) => request<void>("DELETE", `/portal/prompts/${id}`),
  usageSummary: () => request<UsageSummary>("GET", "/portal/usage/summary"),
  usageRecent: () => request<{ events: UsageEvent[] }>("GET", "/portal/usage/recent"),
};
