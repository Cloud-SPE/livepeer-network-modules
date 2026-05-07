// Control-WS client — receives commands from the runner and applies them
// to the scene (`set_expression`, `set_lookat`, `speak`, `clear_speaking`).
//
// Ported from `livepeer-vtuber-project/avatar-renderer/src/controllers/`.

import { decode } from "@msgpack/msgpack";

import type { SceneHandle } from "../scene/boot";

export function connectControlWS(url: string, scene: SceneHandle): WebSocket {
  const ws = new WebSocket(url);
  ws.binaryType = "arraybuffer";
  ws.addEventListener("message", (ev) => {
    if (typeof ev.data === "string") {
      try {
        applyCommand(scene, JSON.parse(ev.data));
      } catch {
        // ignore malformed text frames
      }
      return;
    }
    if (ev.data instanceof ArrayBuffer) {
      try {
        applyCommand(scene, decode(new Uint8Array(ev.data)));
      } catch {
        // ignore malformed binary frames
      }
    }
  });
  return ws;
}

function applyCommand(scene: SceneHandle, msg: unknown): void {
  if (msg === null || typeof msg !== "object") {
    return;
  }
  const m = msg as Record<string, unknown>;
  switch (m["type"]) {
    case "set_expression":
      scene.setExpression(String(m["name"] ?? ""), Number(m["weight"] ?? 0));
      return;
    case "set_lookat":
      scene.setLookAt(
        Number(m["x"] ?? 0),
        Number(m["y"] ?? 0),
        Number(m["z"] ?? 0),
      );
      return;
    case "speak":
      scene.setSpeaking(true);
      return;
    case "clear_speaking":
      scene.setSpeaking(false);
      return;
  }
}
