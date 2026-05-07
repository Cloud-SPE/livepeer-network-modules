// Live worker↔customer WS relay state.
//
// Per Q9 lock the relay is **live-only**; M6 ships no replay buffer.
// On customer disconnect the gateway treats the session as ended
// (matches `cannot_resume` per the events-taxonomy spec). A 30s
// replay buffer is tracked as a follow-up commit.
//
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/runtime/
// http/vtuber/relay.ts:24-27,29-48` (live-only doc + SessionRelay
// interface).

import type { WebSocket } from "ws";

export interface SessionConnections {
  sessionId: string;
  customerId: string;
  customers: Set<WebSocket>;
  worker: WebSocket | null;
}

export interface SessionRelay {
  attachWorker(sessionId: string, customerId: string, ws: WebSocket): void;
  attachCustomer(sessionId: string, customerId: string, ws: WebSocket): void;
  detach(sessionId: string, ws: WebSocket): void;
  endAll(sessionId: string): void;
  size(): number;
  has(sessionId: string): boolean;
  inspect(sessionId: string): SessionConnections | null;
}

export function createSessionRelay(): SessionRelay {
  const sessions = new Map<string, SessionConnections>();

  function ensure(sessionId: string, customerId: string): SessionConnections {
    let entry = sessions.get(sessionId);
    if (entry === undefined) {
      entry = { sessionId, customerId, customers: new Set(), worker: null };
      sessions.set(sessionId, entry);
    }
    return entry;
  }

  return {
    attachWorker(sessionId, customerId, ws) {
      const entry = ensure(sessionId, customerId);
      if (entry.worker !== null && entry.worker !== ws) {
        try {
          entry.worker.close(1012, "replaced");
        } catch {
          // best-effort close
        }
      }
      entry.worker = ws;
    },
    attachCustomer(sessionId, customerId, ws) {
      const entry = ensure(sessionId, customerId);
      entry.customers.add(ws);
    },
    detach(sessionId, ws) {
      const entry = sessions.get(sessionId);
      if (entry === undefined) {
        return;
      }
      if (entry.worker === ws) {
        entry.worker = null;
      }
      entry.customers.delete(ws);
      if (entry.worker === null && entry.customers.size === 0) {
        sessions.delete(sessionId);
      }
    },
    endAll(sessionId) {
      const entry = sessions.get(sessionId);
      if (entry === undefined) {
        return;
      }
      if (entry.worker !== null) {
        try {
          entry.worker.close(1000, "session ended");
        } catch {
          // best-effort close
        }
      }
      for (const ws of entry.customers) {
        try {
          ws.close(1000, "session ended");
        } catch {
          // best-effort close
        }
      }
      sessions.delete(sessionId);
    },
    size() {
      return sessions.size;
    },
    has(sessionId) {
      return sessions.has(sessionId);
    },
    inspect(sessionId) {
      return sessions.get(sessionId) ?? null;
    },
  };
}

export function broadcastToCustomers(
  entry: SessionConnections,
  payload: string | Buffer,
): number {
  let delivered = 0;
  for (const ws of entry.customers) {
    if (ws.readyState === ws.OPEN) {
      ws.send(payload);
      delivered += 1;
    }
  }
  return delivered;
}

export function forwardToWorker(
  entry: SessionConnections,
  payload: string | Buffer,
): boolean {
  if (entry.worker !== null && entry.worker.readyState === entry.worker.OPEN) {
    entry.worker.send(payload);
    return true;
  }
  return false;
}
