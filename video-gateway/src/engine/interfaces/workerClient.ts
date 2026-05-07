import type { SelectedWorkerRoute } from "../types/index.js";

export interface WorkerClient {
  callWorker<TBody, TResp>(opts: {
    route: SelectedWorkerRoute;
    path: string;
    method: "GET" | "POST";
    body?: TBody;
    callerId: string;
    timeoutMs?: number;
  }): Promise<TResp>;
}
