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
    const payload = toBuffer(chunk);
    if (upstreamReady) upstream.write(payload);
    else buffered.push(payload);
  });
  upstream.on("data", (chunk) => {
    client.write(toBuffer(chunk));
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

function toBuffer(chunk: string | Buffer): Buffer {
  return Buffer.isBuffer(chunk) ? chunk : Buffer.from(chunk);
}
