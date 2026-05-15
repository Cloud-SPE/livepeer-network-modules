const STORAGE_KEY = "daydream-admin:creds";

export interface AdminCreds {
  token: string;
  actor: string;
}

export function readCreds(): AdminCreds | null {
  try {
    const raw = sessionStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    return JSON.parse(raw) as AdminCreds;
  } catch {
    return null;
  }
}

export function writeCreds(c: AdminCreds): void {
  sessionStorage.setItem(STORAGE_KEY, JSON.stringify(c));
  window.dispatchEvent(new StorageEvent("storage", { key: STORAGE_KEY }));
}

export function clearCreds(): void {
  sessionStorage.removeItem(STORAGE_KEY);
  window.dispatchEvent(new StorageEvent("storage", { key: STORAGE_KEY }));
}
