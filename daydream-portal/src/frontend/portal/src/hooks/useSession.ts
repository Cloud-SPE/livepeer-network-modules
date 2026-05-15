import { useEffect, useState } from "react";
import { type PortalSession, readSession } from "@/lib/session";

// React doesn't observe sessionStorage natively; the helpers in
// lib/session.ts emit a synthetic StorageEvent on write/clear which
// this hook subscribes to.
export function useSession(): PortalSession | null {
  const [session, setSession] = useState<PortalSession | null>(() => readSession());
  useEffect(() => {
    const onChange = () => setSession(readSession());
    window.addEventListener("storage", onChange);
    return () => window.removeEventListener("storage", onChange);
  }, []);
  return session;
}
