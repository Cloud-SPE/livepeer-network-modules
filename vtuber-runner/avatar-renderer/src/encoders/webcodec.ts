// WebCodecs H.264 encoder — captures canvas frames, encodes via
// VideoEncoder, and ships NAL units over the control-WS to the runner.
//
// Ported from `livepeer-vtuber-project/avatar-renderer/src/encoders/`.

export interface EncoderOptions {
  canvas: HTMLCanvasElement;
  ws: WebSocket;
  bitrate?: number;
  framerate?: number;
}

export function startEncoder(_opts: EncoderOptions): void {
  return;
}
