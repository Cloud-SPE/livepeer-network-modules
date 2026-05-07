export interface RecordingHandoffInput {
  liveStreamId: string;
  recordingStorageKey: string;
  customerId: string;
  projectId: string;
}

export interface RecordingHandoffResult {
  assetId: string;
  liveStreamId: string;
}

export function recordToVodEnabled(sessionParams: {
  record_to_vod?: boolean;
}): boolean {
  return sessionParams.record_to_vod === true;
}

export function planRecordingHandoff(
  input: RecordingHandoffInput,
): RecordingHandoffResult {
  const assetId = `asset_${randomHex16()}`;
  return { assetId, liveStreamId: input.liveStreamId };
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
