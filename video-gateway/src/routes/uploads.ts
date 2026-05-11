import { eq } from "drizzle-orm";
import type { FastifyInstance } from "fastify";
import { z } from "zod";

import type { Config } from "../config.js";
import type { Db } from "../db/pool.js";
import { uploads } from "../db/schema.js";

export interface UploadsDeps {
  cfg: Config;
  videoDb?: Db;
  createUploadLocation?: () => Promise<string>;
  uploadExists?: (id: string) => Promise<boolean>;
  completeUpload?: (id: string) => Promise<boolean>;
}

const CompleteUploadParams = z.object({
  id: z.string().min(1),
});

export function registerUploads(app: FastifyInstance, deps: UploadsDeps): void {
  app.post(deps.cfg.vodTusPath, async (_req, reply) => {
    if (!deps.createUploadLocation) {
      await reply.code(501).send({
        error: "upload_init_unavailable",
        message: "create uploads through the customer portal or a pre-provisioned upload flow",
      });
      return;
    }
    const location = await deps.createUploadLocation();
    await reply.code(201).header("Location", location).send();
  });

  app.head(`${deps.cfg.vodTusPath}/:id`, async (_req, reply) => {
    const parsed = CompleteUploadParams.safeParse(_req.params);
    if (parsed.success && deps.uploadExists && !(await deps.uploadExists(parsed.data.id))) {
      await reply.code(404).send();
      return;
    }
    await reply
      .code(200)
      .header("Tus-Resumable", "1.0.0")
      .header("Upload-Offset", "0")
      .send();
  });

  app.patch(`${deps.cfg.vodTusPath}/:id`, async (req, reply) => {
    const parsed = CompleteUploadParams.safeParse(req.params);
    if (!parsed.success) {
      await reply.code(400).send({ error: "invalid_upload_id", details: parsed.error.issues });
      return;
    }
    if (deps.completeUpload) {
      const completed = await deps.completeUpload(parsed.data.id);
      if (!completed) {
        await reply.code(404).send({ error: "upload_not_found" });
        return;
      }
    } else if (deps.uploadExists && !(await deps.uploadExists(parsed.data.id))) {
      await reply.code(404).send({ error: "upload_not_found" });
      return;
    }
    if (deps.videoDb) {
      await deps.videoDb
        .update(uploads)
        .set({
          status: "completed",
          completedAt: new Date(),
        })
        .where(eq(uploads.id, parsed.data.id));
    }
    await reply
      .code(204)
      .header("Tus-Resumable", "1.0.0")
      .header("Upload-Offset", "0")
      .send();
  });
}
