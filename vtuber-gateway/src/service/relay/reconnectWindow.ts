export interface ReconnectWindowConfig {
  windowMs: number;
  bufferMessages: number;
  bufferBytes: number;
}

export const DEFAULT_RECONNECT_WINDOW_CONFIG: ReconnectWindowConfig = {
  windowMs: 30_000,
  bufferMessages: 64,
  bufferBytes: 1 << 20,
};

interface ReplayEntry {
  seq: number;
  payload: string;
  bytes: number;
}

export interface ReplayBuffer {
  append(seq: number, payload: string): void;
  since(lastSeq: number): readonly ReplayEntry[];
  size(): number;
  bytes(): number;
}

export function createReplayBuffer(
  maxMessages: number,
  maxBytes: number,
): ReplayBuffer {
  const entries: ReplayEntry[] = [];
  let totalBytes = 0;
  return {
    append(seq, payload) {
      const bytes = Buffer.byteLength(payload, "utf8");
      entries.push({ seq, payload, bytes });
      totalBytes += bytes;
      while (entries.length > maxMessages) {
        const dropped = entries.shift();
        if (dropped !== undefined) {
          totalBytes -= dropped.bytes;
        }
      }
      while (maxBytes > 0 && totalBytes > maxBytes && entries.length > 0) {
        const dropped = entries.shift();
        if (dropped !== undefined) {
          totalBytes -= dropped.bytes;
        }
      }
    },
    since(lastSeq) {
      return entries.filter((e) => e.seq > lastSeq);
    },
    size() {
      return entries.length;
    },
    bytes() {
      return totalBytes;
    },
  };
}

export type ReconnectAttachResult =
  | { kind: "attached"; replay: readonly ReplayEntry[]; nextSeq: number }
  | { kind: "conflict"; reason: string };

export interface ReconnectSession {
  sessionId: string;
  active: boolean;
  disconnectedAt: number | null;
  nextSeq: number;
  replay: ReplayBuffer;
  expiryTimer: NodeJS.Timeout | null;
}

export interface ReconnectWindow {
  registerSession(sessionId: string): ReconnectSession;
  hasSession(sessionId: string): boolean;
  attachCustomer(sessionId: string, lastSeq: number): ReconnectAttachResult;
  detachCustomer(sessionId: string): void;
  recordOutbound(sessionId: string, payload: string): number | null;
  endSession(sessionId: string): void;
  inspect(sessionId: string): ReconnectSession | null;
  size(): number;
}

export interface ReconnectWindowOptions {
  cfg: ReconnectWindowConfig;
  onWindowExpiry: (sessionId: string) => void;
  now?: () => number;
  schedule?: (fn: () => void, ms: number) => NodeJS.Timeout;
  cancel?: (handle: NodeJS.Timeout) => void;
}

export function createReconnectWindow(
  opts: ReconnectWindowOptions,
): ReconnectWindow {
  const sessions = new Map<string, ReconnectSession>();
  const now = opts.now ?? (() => Date.now());
  const schedule =
    opts.schedule ??
    ((fn, ms) => setTimeout(fn, ms) as unknown as NodeJS.Timeout);
  const cancel =
    opts.cancel ??
    ((handle) => {
      clearTimeout(handle);
    });

  return {
    registerSession(sessionId) {
      const existing = sessions.get(sessionId);
      if (existing !== undefined) {
        return existing;
      }
      const sess: ReconnectSession = {
        sessionId,
        active: false,
        disconnectedAt: null,
        nextSeq: 0,
        replay: createReplayBuffer(
          opts.cfg.bufferMessages,
          opts.cfg.bufferBytes,
        ),
        expiryTimer: null,
      };
      sessions.set(sessionId, sess);
      return sess;
    },
    hasSession(sessionId) {
      return sessions.has(sessionId);
    },
    attachCustomer(sessionId, lastSeq) {
      const sess = sessions.get(sessionId);
      if (sess === undefined) {
        return { kind: "conflict", reason: "session_unknown" };
      }
      if (sess.active) {
        return { kind: "conflict", reason: "already_attached" };
      }
      if (sess.expiryTimer !== null) {
        cancel(sess.expiryTimer);
        sess.expiryTimer = null;
      }
      sess.active = true;
      sess.disconnectedAt = null;
      const replay = sess.replay.since(lastSeq);
      return { kind: "attached", replay, nextSeq: sess.nextSeq };
    },
    detachCustomer(sessionId) {
      const sess = sessions.get(sessionId);
      if (sess === undefined) {
        return;
      }
      if (!sess.active) {
        return;
      }
      sess.active = false;
      sess.disconnectedAt = now();
      if (sess.expiryTimer !== null) {
        cancel(sess.expiryTimer);
      }
      sess.expiryTimer = schedule(() => {
        const cur = sessions.get(sessionId);
        if (cur === undefined) {
          return;
        }
        if (cur.active) {
          return;
        }
        sessions.delete(sessionId);
        cur.expiryTimer = null;
        opts.onWindowExpiry(sessionId);
      }, opts.cfg.windowMs);
    },
    recordOutbound(sessionId, payload) {
      const sess = sessions.get(sessionId);
      if (sess === undefined) {
        return null;
      }
      sess.nextSeq += 1;
      sess.replay.append(sess.nextSeq, payload);
      return sess.nextSeq;
    },
    endSession(sessionId) {
      const sess = sessions.get(sessionId);
      if (sess === undefined) {
        return;
      }
      if (sess.expiryTimer !== null) {
        cancel(sess.expiryTimer);
        sess.expiryTimer = null;
      }
      sessions.delete(sessionId);
    },
    inspect(sessionId) {
      return sessions.get(sessionId) ?? null;
    },
    size() {
      return sessions.size;
    },
  };
}

export function parseLastSeqHeader(header: unknown): number {
  if (typeof header !== "string" || header.length === 0) {
    return 0;
  }
  const n = Number.parseInt(header, 10);
  if (!Number.isFinite(n) || n < 0) {
    return 0;
  }
  return n;
}
