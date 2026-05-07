import type { Capability } from "./caller.js";

export interface SelectedWorkerRoute {
  workerUrl: string;
  ethAddress: string;
  capability: Capability;
  offering: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra?: Record<string, unknown>;
  constraints?: Record<string, unknown>;
}

export interface WorkerInventoryEntry {
  url: string;
  capabilities: Capability[];
  gpu?: string;
  region?: string;
}
