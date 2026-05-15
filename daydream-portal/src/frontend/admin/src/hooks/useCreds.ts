import { useEffect, useState } from "react";
import { type AdminCreds, readCreds } from "@/lib/creds";

export function useCreds(): AdminCreds | null {
  const [creds, setCreds] = useState<AdminCreds | null>(() => readCreds());
  useEffect(() => {
    const onChange = () => setCreds(readCreds());
    window.addEventListener("storage", onChange);
    return () => window.removeEventListener("storage", onChange);
  }, []);
  return creds;
}
