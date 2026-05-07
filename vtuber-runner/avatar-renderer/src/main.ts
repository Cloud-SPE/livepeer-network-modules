// Browser entry-point for the vtuber avatar renderer.
//
// Loaded by the runner's headless Chromium child via:
//   http://localhost:<port>/?vrm=<url>&ws=<ws_url>&width=<n>&height=<n>
//
// Ported from `livepeer-vtuber-project/avatar-renderer/src/main.ts:1-24`
// (query-param + status-reporting docs). Per OQ1 lock the bundle is
// rebuilt from source in stage 1 of the runner Dockerfile; no pre-built
// dist artifact is published.

import { bootScene } from "./scene/boot";
import { connectControlWS } from "./controllers/control-ws";
import { startEncoder } from "./encoders/webcodec";

const params = new URLSearchParams(window.location.search);

const vrmUrl = params.get("vrm") ?? "";
const wsUrl = params.get("ws") ?? "";
const width = parseInt(params.get("width") ?? "1280", 10);
const height = parseInt(params.get("height") ?? "720", 10);

async function main(): Promise<void> {
  const stage = document.getElementById("stage") as HTMLCanvasElement | null;
  if (stage === null) {
    throw new Error("missing #stage canvas");
  }
  stage.width = width;
  stage.height = height;

  const scene = await bootScene({ canvas: stage, vrmUrl, width, height });
  const ws = wsUrl !== "" ? connectControlWS(wsUrl, scene) : null;
  if (ws !== null) {
    startEncoder({ canvas: stage, ws });
  }
}

void main();
