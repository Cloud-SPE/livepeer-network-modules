import type { Capability, SelectedWorkerRoute } from "../types/index.js";

export interface WorkerResolver {
  selectWorker(opts: {
    capability: Capability;
    offering: string;
    tier: string;
    minWeight?: number;
  }): Promise<SelectedWorkerRoute | null>;
}
