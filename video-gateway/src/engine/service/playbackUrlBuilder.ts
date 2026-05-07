import type { PlaybackId } from "../types/index.js";

export function buildPlaybackUrl(opts: {
  playbackId: PlaybackId;
  baseUrl: string;
  signedToken?: string;
}): string {
  const base = opts.baseUrl.replace(/\/$/, "");
  const url = `${base}/${opts.playbackId.id}.m3u8`;
  if (opts.playbackId.policy === "signed" && opts.signedToken) {
    return `${url}?token=${encodeURIComponent(opts.signedToken)}`;
  }
  return url;
}
