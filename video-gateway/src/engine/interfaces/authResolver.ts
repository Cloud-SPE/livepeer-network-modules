import type { Caller } from "../types/index.js";

export interface AuthResolver {
  resolve(req: {
    headers: Record<string, string | undefined>;
    ip: string;
  }): Promise<Caller | null>;
}

export interface AdminAuthResolver {
  resolve(req: {
    headers: Record<string, string | undefined>;
    ip: string;
  }): Promise<{ actor: string } | null>;
}
