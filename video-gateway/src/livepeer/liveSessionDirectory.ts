export interface LiveSessionRoute {
  sessionId: string;
  brokerUrl: string;
  hlsPlaybackUrl: string;
}

export interface LiveSessionDirectory {
  record(route: LiveSessionRoute): void;
  get(sessionId: string): LiveSessionRoute | null;
}

export function createLiveSessionDirectory(): LiveSessionDirectory {
  const routes = new Map<string, LiveSessionRoute>();
  return {
    record(route: LiveSessionRoute): void {
      routes.set(route.sessionId, route);
    },
    get(sessionId: string): LiveSessionRoute | null {
      return routes.get(sessionId) ?? null;
    },
  };
}
