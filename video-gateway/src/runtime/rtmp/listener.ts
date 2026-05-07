import { createServer, type Server, type Socket } from "node:net";

import type { Config } from "../../config.js";
import { bridgeRtmpStream } from "./proxy.js";

export interface RtmpListenerDeps {
  cfg: Config;
  onClient?: (sock: Socket) => void;
}

export function createRtmpListener(deps: RtmpListenerDeps): Server {
  const [_host, portStr] = deps.cfg.rtmpListenAddr.split(":");
  const port = parseInt(portStr ?? "1935", 10);
  const [brokerHost, brokerPortStr] = deps.cfg.brokerRtmpHost.split(":");
  const brokerPort = parseInt(brokerPortStr ?? "1935", 10);

  const server = createServer((sock) => {
    deps.onClient?.(sock);
    bridgeRtmpStream(sock, {
      brokerHost: brokerHost ?? "broker",
      brokerPort,
    });
  });

  server.listen(port);
  return server;
}
