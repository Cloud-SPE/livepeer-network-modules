/**
 * `multipart-audio-duration/v1` — the canonical estimator.
 *
 * A transcription offering bills by the playing time of the audio it was
 * given. A caller that needs to reserve funds BEFORE the work runs has
 * to reach the same number the broker will bill, so this is a port of
 * the broker's implementation pinned to it by shared fixtures rather
 * than a second opinion about the same files.
 *
 * The estimate is a CEILING and never settlement evidence. The signed
 * settlement the broker returns is authoritative for what actually
 * happened; this only bounds what may happen.
 */

export const ESTIMATOR = 'multipart-audio-duration/v1' as const;
export const ROUNDING = 'ceil-to-whole-seconds' as const;

export {
  probeDuration,
  UnsupportedFormatError,
  MalformedError,
  InexactError,
  type DurationResult,
  type Format,
} from './probe.js';

import { probeDuration, InexactError } from './probe.js';
import { extractFilePart } from './multipart.js';

/**
 * The estimator contract: an exact whole-second ceiling, or a throw.
 *
 * It refuses rather than guesses. A ceiling is a number somebody funds
 * against — one that reads low underfunds real work, one that reads high
 * overcharges — so a duration that cannot be measured is not offered at
 * all. In practice only headerless constant-bitrate MP3 lands here.
 *
 * Rounds UP: a rule that could return 0 for delivered work funds nothing
 * for it.
 */
export function estimateCeilingSeconds(bytes: Uint8Array): number {
  const res = probeDuration(bytes);
  if (!res.exact) throw new InexactError(res.format);
  return Math.ceil(res.seconds);
}

/**
 * Same contract, reading the audio out of a multipart body first.
 *
 * A non-multipart body is treated as the audio itself, so one entry
 * point serves both a form upload and a raw one.
 */
export function estimateCeilingSecondsFromMultipart(
  body: Uint8Array,
  contentType: string,
  fileField = 'file',
): number {
  return estimateCeilingSeconds(extractFilePart(body, contentType, fileField));
}
