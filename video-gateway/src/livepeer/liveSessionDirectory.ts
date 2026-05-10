export interface LiveSessionRoute {
  streamId: string;
  sessionId: string;
  brokerUrl: string;
  brokerRtmpUrl: string;
  streamKey: string;
  hlsPlaybackUrl: string;
}

export interface LiveSessionDirectory {
  record(route: LiveSessionRoute): void;
  get(sessionId: string): LiveSessionRoute | null;
  getByStreamId(streamId: string): LiveSessionRoute | null;
}

export function createLiveSessionDirectory(): LiveSessionDirectory {
  const routesBySessionId = new Map<string, LiveSessionRoute>();
  const routesByStreamId = new Map<string, LiveSessionRoute>();
  return {
    record(route: LiveSessionRoute): void {
      routesBySessionId.set(route.sessionId, route);
      routesByStreamId.set(route.streamId, route);
    },
    get(sessionId: string): LiveSessionRoute | null {
      return routesBySessionId.get(sessionId) ?? null;
    },
    getByStreamId(streamId: string): LiveSessionRoute | null {
      return routesByStreamId.get(streamId) ?? null;
    },
  };
}
