import WebSocket from "ws";

const MODE = "session-control-plus-media@v0";
const SPEC_VERSION = "0.1";
const VTUBER_CAPABILITY = "livepeer:vtuber-session";

export interface WorkerSessionStartRequest {
  session_id: string;
  persona: string;
  vrm_url: string;
  llm_provider: string;
  tts_provider: string;
  target_youtube_broadcast?: string;
  width: number;
  height: number;
  target_fps: number;
  worker_control_bearer: string;
  extras?: Record<string, string>;
}

export interface WorkerSessionStartResponse {
  session_id: string;
  status: "starting" | "active" | "ending" | "ended" | "errored";
  started_at: string;
  control_url?: string;
  expires_at?: string;
  media?: Record<string, unknown>;
}

export interface WorkerSessionTopupRequest {
  sessionId: string;
  paymentHeader: string;
}

export interface WorkerStartSessionInput {
  request: WorkerSessionStartRequest;
  paymentHeader: string;
  offering: string;
}

export interface WorkerClient {
  startSession(
    nodeUrl: string,
    input: WorkerStartSessionInput,
  ): Promise<WorkerSessionStartResponse>;
  stopSession(nodeUrl: string, sessionId: string): Promise<void>;
  topupSession(nodeUrl: string, req: WorkerSessionTopupRequest): Promise<void>;
}

export function createBrokerWorkerClient(): WorkerClient {
  return {
    async startSession(
      nodeUrl: string,
      input: WorkerStartSessionInput,
    ): Promise<WorkerSessionStartResponse> {
      const url = `${nodeUrl.replace(/\/$/, "")}/v1/cap`;
      const res = await fetch(url, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Livepeer-Capability": VTUBER_CAPABILITY,
          "Livepeer-Offering": input.offering,
          "Livepeer-Mode": MODE,
          "Livepeer-Spec-Version": SPEC_VERSION,
          "Livepeer-Payment": input.paymentHeader,
        },
        body: JSON.stringify(input.request),
      });
      if (!res.ok) {
        throw new Error(`vtuber session-open failed: ${res.status}`);
      }
      const payload = (await res.json()) as {
        session_id?: string;
        control_url?: string;
        expires_at?: string;
        media?: Record<string, unknown>;
      };
      if (!payload.session_id || !payload.control_url || !payload.expires_at) {
        throw new Error("vtuber session-open returned malformed response");
      }
      return {
        session_id: payload.session_id,
        status: "active",
        started_at: new Date().toISOString(),
        control_url: payload.control_url,
        expires_at: payload.expires_at,
        ...(payload.media ? { media: payload.media } : {}),
      };
    },
    async stopSession(nodeUrl: string, sessionId: string): Promise<void> {
      const controlUrl = deriveControlUrl(nodeUrl, sessionId);
      await sendControlEnvelope(controlUrl, { type: "session.end" });
    },
    async topupSession(
      nodeUrl: string,
      req: WorkerSessionTopupRequest,
    ): Promise<void> {
      const controlUrl = deriveControlUrl(nodeUrl, req.sessionId);
      await sendControlEnvelope(controlUrl, {
        type: "session.topup",
        body: {
          payment_header: req.paymentHeader,
        },
      });
    },
  };
}

function deriveControlUrl(nodeUrl: string, sessionId: string): string {
  const url = new URL(nodeUrl);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  url.pathname = `/v1/cap/${sessionId}/control`;
  url.search = "";
  return url.toString();
}

async function sendControlEnvelope(
  controlUrl: string,
  envelope: Record<string, unknown>,
): Promise<void> {
  await new Promise<void>((resolve, reject) => {
    const ws = new WebSocket(controlUrl);
    ws.once("open", () => {
      ws.send(JSON.stringify(envelope), (err) => {
        if (err) {
          ws.close();
          reject(err);
          return;
        }
        ws.close();
        resolve();
      });
    });
    ws.once("error", reject);
  });
}
