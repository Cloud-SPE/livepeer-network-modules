export interface Recording {
  callerId: string;
  capability: string;
  offering: string;
  workUnits: bigint;
  expectedValueWei: bigint;
  recordedAt: number;
}

export interface Recorder {
  record(input: {
    callerId: string;
    capability: string;
    offering: string;
    workUnits: bigint;
    expectedValueWei: bigint;
  }): void;
  drain(): Recording[];
  size(): number;
}

export interface CreateRecorderInput {
  now?: () => number;
  capacity?: number;
}

export function createRecorder(input: CreateRecorderInput = {}): Recorder {
  const now = input.now ?? Date.now;
  const capacity = input.capacity ?? 10_000;
  const buffer: Recording[] = [];

  return {
    record(rec) {
      if (buffer.length >= capacity) buffer.shift();
      buffer.push({ ...rec, recordedAt: now() });
    },
    drain() {
      const out = buffer.slice();
      buffer.length = 0;
      return out;
    },
    size() {
      return buffer.length;
    },
  };
}
