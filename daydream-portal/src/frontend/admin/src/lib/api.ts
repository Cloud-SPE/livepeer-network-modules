// Admin HTTP client. customer-portal's static admin resolver expects
// the standard Authorization: Bearer header plus X-Actor for the
// audit-log actor.

import { readCreds } from "./creds";

export class ApiError extends Error {
  constructor(public status: number, public body: unknown) {
    super(
      typeof body === "string"
        ? body
        : (body as { message?: string } | { error?: { message?: string } })
          ? JSON.stringify(body)
          : `HTTP ${status}`,
    );
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const creds = readCreds();
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["content-type"] = "application/json";
  if (creds) {
    headers["authorization"] = `Bearer ${creds.token}`;
    headers["x-actor"] = creds.actor;
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

export interface WaitlistEntry {
  id: string;
  email: string;
  display_name: string | null;
  reason: string | null;
  status: "pending" | "approved" | "rejected";
  customer_id: string | null;
  decided_by: string | null;
  decided_at: string | null;
  created_at: string;
}

export interface AdminUsageSummary {
  window_days: number;
  total_sessions: number;
  total_seconds: number;
  unique_customers: number;
}

export interface ApproveResult {
  waitlist_id: string;
  customer_id: string;
  api_key_id: string;
  api_key: string;
  warning: string;
}

export const adminApi = {
  listWaitlist: (status?: WaitlistEntry["status"]) =>
    request<{ entries: WaitlistEntry[] }>(
      "GET",
      `/admin/waitlist${status ? `?status=${status}` : ""}`,
    ),
  approve: (id: string, key_label?: string) =>
    request<ApproveResult>("POST", `/admin/waitlist/${id}/approve`, { key_label }),
  reject: (id: string, reason?: string) =>
    request<{ waitlist_id: string; status: string }>(
      "POST",
      `/admin/waitlist/${id}/reject`,
      { reason },
    ),
  usageSummary: () => request<AdminUsageSummary>("GET", "/admin/usage/summary"),
};
