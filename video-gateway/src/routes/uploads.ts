import type { FastifyInstance } from "fastify";

import type { Config } from "../config.js";

export interface UploadsDeps {
  cfg: Config;
}

export function registerUploads(app: FastifyInstance, deps: UploadsDeps): void {
  app.post(deps.cfg.vodTusPath, async (_req, reply) => {
    const uploadId = `upload_${randomHex16()}`;
    await reply.code(201).header("Location", `${deps.cfg.vodTusPath}/${uploadId}`).send();
  });

  app.head(`${deps.cfg.vodTusPath}/:id`, async (_req, reply) => {
    await reply
      .code(200)
      .header("Tus-Resumable", "1.0.0")
      .header("Upload-Offset", "0")
      .send();
  });

  app.patch(`${deps.cfg.vodTusPath}/:id`, async (_req, reply) => {
    await reply
      .code(204)
      .header("Tus-Resumable", "1.0.0")
      .header("Upload-Offset", "0")
      .send();
  });
}

function randomHex16(): string {
  return Math.random().toString(16).slice(2, 18).padEnd(16, "0");
}
