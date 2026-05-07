import { connect, type Socket } from "node:net";

export interface ProxyOptions {
  brokerHost: string;
  brokerPort: number;
}

export function bridgeRtmpStream(client: Socket, opts: ProxyOptions): void {
  const upstream = connect({ host: opts.brokerHost, port: opts.brokerPort });
  let upstreamReady = false;
  const buffered: Buffer[] = [];

  upstream.on("connect", () => {
    upstreamReady = true;
    for (const chunk of buffered.splice(0)) upstream.write(chunk);
  });

  client.on("data", (chunk) => {
    if (upstreamReady) upstream.write(chunk);
    else buffered.push(chunk);
  });
  upstream.on("data", (chunk) => {
    client.write(chunk);
  });

  const finish = (): void => {
    try {
      client.destroy();
    } catch {
      /* ignored */
    }
    try {
      upstream.destroy();
    } catch {
      /* ignored */
    }
  };

  client.on("end", finish);
  client.on("error", finish);
  upstream.on("end", finish);
  upstream.on("error", finish);
}
