import type { FastifyInstance, FastifyReply, FastifyRequest } from "fastify";

import { readOrSynthRequestId } from "../livepeer/requestId.js";
import { HEADER } from "../livepeer/headers.js";
import type { Config } from "../config.js";

export function registerAudioSpeech(app: FastifyInstance, cfg: Config): void {
  app.post("/v1/audio/speech", async (req: FastifyRequest, reply: FastifyReply) => {
    const requestId = readOrSynthRequestId(req);
    if (!cfg.audioSpeechEnabled) {
      await reply
        .code(503)
        .header("Content-Type", "application/json")
        .header(HEADER.REQUEST_ID, requestId)
        .header(HEADER.ERROR, "mode_unsupported")
        .send({
          error: "mode_unsupported",
          message:
            "/v1/audio/speech requires http-binary-stream@v0 which is not yet defined; set OPENAI_AUDIO_SPEECH_ENABLED=true once the mode ships",
        });
      return;
    }
    await reply
      .code(501)
      .header("Content-Type", "application/json")
      .header(HEADER.REQUEST_ID, requestId)
      .send({
        error: "not_implemented",
        message: "/v1/audio/speech wire path is not yet implemented",
      });
  });
}
