// Local session: the UI token we received from /portal/login-by-key,
// plus the actor label the user typed at sign-in.

const STORAGE_KEY = "daydream-portal:session";

export interface PortalSession {
  token: string;
  actor: string;
  customerId: string;
  email: string;
}

export function readSession(): PortalSession | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as PortalSession;
  } catch {
    return null;
  }
}

export function writeSession(session: PortalSession): void {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(session));
  // React state isn't watching sessionStorage directly; emit a storage
  // event so components subscribed via useSession() pick up the change.
  window.dispatchEvent(new StorageEvent("storage", { key: STORAGE_KEY }));
}

export function clearSession(): void {
  sessionStorage.removeItem(STORAGE_KEY);
  window.dispatchEvent(new StorageEvent("storage", { key: STORAGE_KEY }));
}
