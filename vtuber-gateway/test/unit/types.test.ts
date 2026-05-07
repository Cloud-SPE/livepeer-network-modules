import test from "node:test";
import assert from "node:assert/strict";

import { SessionOpenRequestSchema } from "../../src/types/vtuber.js";

test("SessionOpenRequestSchema applies width/height/fps defaults", () => {
  const req = SessionOpenRequestSchema.parse({
    persona: "grifter",
    vrm_url: "https://example.com/avatar.vrm",
    llm_provider: "livepeer",
    tts_provider: "livepeer",
  });
  assert.equal(req.width, 1280);
  assert.equal(req.height, 720);
  assert.equal(req.target_fps, 24);
});

test("SessionOpenRequestSchema rejects out-of-range fps", () => {
  assert.throws(() =>
    SessionOpenRequestSchema.parse({
      persona: "grifter",
      vrm_url: "https://example.com/avatar.vrm",
      llm_provider: "livepeer",
      tts_provider: "livepeer",
      target_fps: 120,
    }),
  );
});
