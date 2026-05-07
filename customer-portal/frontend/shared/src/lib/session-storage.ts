export interface SessionLike {
  customerId: string;
  email: string;
}

const SESSION_KEY = 'customer-portal:session';

export function readSession(): SessionLike | null {
  try {
    const raw = sessionStorage.getItem(SESSION_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as SessionLike;
  } catch {
    return null;
  }
}

export function writeSession(session: SessionLike): void {
  sessionStorage.setItem(SESSION_KEY, JSON.stringify(session));
}

export function clearSession(): void {
  sessionStorage.removeItem(SESSION_KEY);
}
