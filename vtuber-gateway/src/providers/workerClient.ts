// HTTP client for `POST /api/sessions/start` on the runner.
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/providers/workerClient.ts`.

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
}

export interface WorkerSessionTopupRequest {
  sessionId: string;
  paymentHeader: string;
}

export interface WorkerStartSessionInput {
  request: WorkerSessionStartRequest;
  paymentHeader: string;
}

export interface WorkerClient {
  startSession(
    nodeUrl: string,
    input: WorkerStartSessionInput,
  ): Promise<WorkerSessionStartResponse>;
  stopSession(nodeUrl: string, sessionId: string): Promise<void>;
  topupSession(nodeUrl: string, req: WorkerSessionTopupRequest): Promise<void>;
}
