export interface SessionLike {
  token: string;
  actor: string;
  customerId?: string;
  email?: string;
}

const SESSION_KEY = 'customer-portal:session';

function currentSessionKey(): string {
  if (typeof document === 'undefined') return SESSION_KEY;
  const bodyKey = document.body?.dataset.livepeerSessionKey?.trim();
  if (bodyKey) return bodyKey;
  const htmlKey = document.documentElement?.dataset.livepeerSessionKey?.trim();
  if (htmlKey) return htmlKey;
  return SESSION_KEY;
}

export function readSession(): SessionLike | null {
  try {
    const raw = sessionStorage.getItem(currentSessionKey());
    if (!raw) return null;
    return JSON.parse(raw) as SessionLike;
  } catch {
    return null;
  }
}

export function writeSession(session: SessionLike): void {
  sessionStorage.setItem(currentSessionKey(), JSON.stringify(session));
}

export function clearSession(): void {
  sessionStorage.removeItem(currentSessionKey());
}
