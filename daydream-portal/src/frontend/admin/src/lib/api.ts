// Admin HTTP client. Admin auth is via two headers: X-Admin-Token and
// X-Admin-Actor (the operator's name for the audit log). The shared
// ApiClient routes its own bearer + actor headers, but the admin
// resolver in customer-portal expects different header names — so we
// roll a slim client of our own here.

export interface AdminCreds {
  token: string;
  actor: string;
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

const STORAGE_KEY = "daydream-admin:creds";

export function readCreds(): AdminCreds | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as AdminCreds;
  } catch {
    return null;
  }
}

export function writeCreds(creds: AdminCreds): void {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(creds));
}

export function clearCreds(): void {
  sessionStorage.removeItem(STORAGE_KEY);
}

export class DaydreamAdminApi {
  constructor(private baseUrl: string = "") {}

  private headers(): Record<string, string> {
    const creds = readCreds();
    const h: Record<string, string> = { "content-type": "application/json" };
    if (creds) {
      // customer-portal's static admin resolver consumes the standard
      // Authorization: Bearer header plus X-Actor for the audit log.
      h["authorization"] = `Bearer ${creds.token}`;
      h["x-actor"] = creds.actor;
    }
    return h;
  }

  private async request<T>(
    method: string,
    path: string,
    body?: unknown,
  ): Promise<T> {
    const res = await fetch(this.baseUrl + path, {
      method,
      headers: this.headers(),
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
    if (!res.ok) {
      throw new Error(
        `[${res.status}] ${typeof parsed === "string" ? parsed : JSON.stringify(parsed)}`,
      );
    }
    return parsed as T;
  }

  listWaitlist(status?: WaitlistEntry["status"]): Promise<{
    entries: WaitlistEntry[];
  }> {
    const q = status ? `?status=${encodeURIComponent(status)}` : "";
    return this.request("GET", `/admin/waitlist${q}`);
  }

  approveWaitlist(
    id: string,
    keyLabel?: string,
  ): Promise<{
    waitlist_id: string;
    customer_id: string;
    api_key_id: string;
    api_key: string;
    warning: string;
  }> {
    return this.request("POST", `/admin/waitlist/${id}/approve`, {
      key_label: keyLabel,
    });
  }

  rejectWaitlist(id: string, reason?: string): Promise<{
    waitlist_id: string;
    status: string;
  }> {
    return this.request("POST", `/admin/waitlist/${id}/reject`, { reason });
  }

  usageSummary(): Promise<AdminUsageSummary> {
    return this.request("GET", "/admin/usage/summary");
  }
}
