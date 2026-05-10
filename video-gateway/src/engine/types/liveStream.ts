import type { Capability } from "./caller.js";

export interface LiveStream {
  id: string;
  projectId: string;
  name?: string;
  streamKeyHash: string;
  status: LiveStreamStatus;
  ingestProtocol: IngestProtocol;
  recordingEnabled: boolean;
  sessionId?: string;
  workerId?: string;
  workerUrl?: string;
  selectedCapability?: Capability;
  selectedOffering?: string;
  selectedWorkUnit?: string;
  selectedPricePerWorkUnitWei?: string;
  recordingAssetId?: string;
  lastSeenAt?: Date;
  createdAt: Date;
  endedAt?: Date;
}

export type LiveStreamStatus =
  | "idle"
  | "active"
  | "reconnecting"
  | "ended"
  | "recording_processing"
  | "recording_ready"
  | "errored";

export type IngestProtocol = "rtmp";
