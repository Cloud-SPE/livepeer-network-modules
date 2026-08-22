/**
 * Container parsers for multipart-audio-duration/v1.
 *
 * A port of the broker's Go implementation, pinned to it by the shared
 * fixtures in
 * `livepeer-network-protocol/extractors/fixtures/multipart-audio-duration-v1`.
 * Both sides run those, because the point of two implementations is that
 * a client can compute a funding ceiling BEFORE the work runs and the
 * broker can bill from the same parse afterwards. If they disagree, the
 * settlement exceeds the ceiling and a correct exchange is refused.
 */

export type Format = 'wav' | 'flac' | 'mp4' | 'ogg' | 'mp3' | 'webm';

export interface DurationResult {
  seconds: number;
  format: Format;
  /**
   * False when the duration came from a bitrate estimate rather than a
   * declared sample or frame count. Only constant-bitrate MP3 without a
   * Xing/VBRI header reaches this.
   */
  exact: boolean;
}

export class UnsupportedFormatError extends Error {
  constructor(message = 'unrecognized audio container') {
    super(message);
    this.name = 'UnsupportedFormatError';
  }
}

export class MalformedError extends Error {
  constructor(message: string) {
    super(`container recognized but duration unreadable: ${message}`);
    this.name = 'MalformedError';
  }
}

export class InexactError extends Error {
  constructor(format: Format) {
    super(`${format} duration is an estimate, not a measurement`);
    this.name = 'InexactError';
  }
}

const MAX_PLAUSIBLE_SECONDS = 24 * 60 * 60;

function ascii(b: Uint8Array, start: number, len: number): string {
  return String.fromCharCode(...b.subarray(start, start + len));
}

function checkSeconds(sec: number): number {
  if (!Number.isFinite(sec) || sec < 0) {
    throw new MalformedError(`implausible duration ${sec}`);
  }
  // Beyond a day is a misparse rather than a transcription input, and
  // funding it would be a large silent overcharge.
  if (sec > MAX_PLAUSIBLE_SECONDS) {
    throw new MalformedError(`implausible duration ${Math.round(sec)}s`);
  }
  return sec;
}

/** Measure an audio file's duration. */
export function probeDuration(bytes: Uint8Array): DurationResult {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);

  if (bytes.length >= 12 && ascii(bytes, 0, 4) === 'RIFF' && ascii(bytes, 8, 4) === 'WAVE') {
    return probeWav(bytes, view);
  }
  if (bytes.length >= 4 && ascii(bytes, 0, 4) === 'fLaC') {
    return probeFlac(bytes, view);
  }
  if (bytes.length >= 4 && ascii(bytes, 0, 4) === 'OggS') {
    return probeOgg(bytes, view);
  }
  if (bytes.length >= 8 && ascii(bytes, 4, 4) === 'ftyp') {
    return probeMp4(bytes, view);
  }
  if (
    bytes.length >= 4 &&
    bytes[0] === 0x1a && bytes[1] === 0x45 && bytes[2] === 0xdf && bytes[3] === 0xa3
  ) {
    return probeMatroska(bytes, view);
  }
  if (bytes.length >= 3 && ascii(bytes, 0, 3) === 'ID3') return probeMp3(bytes, view);
  if (isMp3FrameSync(bytes, 0)) return probeMp3(bytes, view);

  throw new UnsupportedFormatError();
}

// --- WAV: data chunk over the fmt byte rate -------------------------------

function probeWav(b: Uint8Array, v: DataView): DurationResult {
  let byteRate = 0;
  let dataSize = 0;
  let sawFmt = false;
  let sawData = false;

  let pos = 12;
  while (pos + 8 <= b.length) {
    const id = ascii(b, pos, 4);
    const size = v.getUint32(pos + 4, true);
    const body = pos + 8;
    if (id === 'fmt ') {
      if (body + 16 > b.length) throw new MalformedError('fmt chunk truncated');
      byteRate = v.getUint32(body + 8, true);
      sawFmt = true;
    } else if (id === 'data') {
      dataSize = size;
      // A streamed WAV can declare size 0 and run to EOF; measure what
      // actually arrived rather than trusting the header.
      if (dataSize === 0 || body + dataSize > b.length) dataSize = b.length - body;
      sawData = true;
    }
    if (sawFmt && sawData) break;
    let adv = size + 8;
    if (size % 2 === 1) adv++;
    if (adv <= 8) throw new MalformedError('zero-length chunk');
    pos += adv;
  }
  if (!sawFmt || !sawData || byteRate === 0) throw new MalformedError('missing fmt or data');
  return { seconds: checkSeconds(dataSize / byteRate), format: 'wav', exact: true };
}

// --- FLAC: STREAMINFO declares samples and rate ---------------------------

function probeFlac(b: Uint8Array, v: DataView): DurationResult {
  if (b.length < 4 + 4 + 34) throw new MalformedError('too short for STREAMINFO');
  if ((b[4] & 0x7f) !== 0) throw new MalformedError('first block is not STREAMINFO');
  // sampleRate(20) channels(3) bitsPerSample(5) totalSamples(36)
  const packed = v.getBigUint64(8 + 10);
  const sampleRate = Number(packed >> 44n);
  const totalSamples = Number(packed & 0xfffffffffn);
  if (sampleRate === 0) throw new MalformedError('zero sample rate');
  if (totalSamples === 0) {
    // Legal, and means "unknown". Refuse rather than report zero for
    // real audio.
    throw new MalformedError('FLAC declares no sample count');
  }
  return { seconds: checkSeconds(totalSamples / sampleRate), format: 'flac', exact: true };
}

// --- MP4 / M4A: moov -> mvhd ----------------------------------------------

function findBox(b: Uint8Array, v: DataView, want: string): Uint8Array | null {
  let pos = 0;
  while (pos + 8 <= b.length) {
    let size = v.getUint32(pos);
    const typ = ascii(b, pos + 4, 4);
    let body = pos + 8;
    if (size === 0) {
      size = b.length - pos;
    } else if (size === 1) {
      if (body + 8 > b.length) return null;
      size = Number(v.getBigUint64(body));
      body += 8;
    }
    if (size < 8 || pos + size > b.length) {
      if (typ === want && body < b.length) return b.subarray(body);
      return null;
    }
    if (typ === want) return b.subarray(body, pos + size);
    pos += size;
  }
  return null;
}

function probeMp4(b: Uint8Array, v: DataView): DurationResult {
  const moov = findBox(b, v, 'moov');
  if (!moov) throw new MalformedError('no moov box');
  const mv = new DataView(moov.buffer, moov.byteOffset, moov.byteLength);
  const mvhd = findBox(moov, mv, 'mvhd');
  if (!mvhd) throw new MalformedError('no mvhd box');
  const hv = new DataView(mvhd.buffer, mvhd.byteOffset, mvhd.byteLength);

  let timescale: number;
  let duration: number;
  switch (mvhd[0]) {
    case 0:
      if (mvhd.length < 20) throw new MalformedError('mvhd v0 truncated');
      timescale = hv.getUint32(12);
      duration = hv.getUint32(16);
      break;
    case 1:
      if (mvhd.length < 32) throw new MalformedError('mvhd v1 truncated');
      timescale = hv.getUint32(20);
      duration = Number(hv.getBigUint64(24));
      break;
    default:
      throw new MalformedError(`mvhd version ${mvhd[0]}`);
  }
  if (timescale === 0) throw new MalformedError('zero timescale');
  // A fragmented MP4 declares 0 here and carries the real length in
  // fragments. Refusing beats reporting zero for real audio.
  if (duration === 0) throw new MalformedError('mvhd duration is 0 (fragmented MP4?)');
  return { seconds: checkSeconds(duration / timescale), format: 'mp4', exact: true };
}

// --- Ogg: last page granule over the identification rate ------------------

function indexOf(hay: Uint8Array, needle: string): number {
  outer: for (let i = 0; i + needle.length <= hay.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (hay[i + j] !== needle.charCodeAt(j)) continue outer;
    }
    return i;
  }
  return -1;
}

function probeOgg(b: Uint8Array, v: DataView): DurationResult {
  let rate = 0;
  if (indexOf(b, 'OpusHead') >= 0) {
    // Opus granule positions are ALWAYS at 48 kHz regardless of the
    // original input rate.
    rate = 48000;
  } else {
    const i = indexOf(b, '\x01vorbis');
    if (i >= 0 && i + 16 <= b.length) rate = v.getUint32(i + 12, true);
  }
  if (rate === 0) throw new MalformedError('no Opus or Vorbis identification header');

  let granule = 0;
  let found = false;
  for (let i = 0; i + 27 <= b.length; ) {
    if (ascii(b, i, 4) !== 'OggS') {
      i++;
      continue;
    }
    const g = v.getBigUint64(i + 6, true);
    const segCount = b[i + 26];
    if (i + 27 + segCount > b.length) break;
    let payload = 0;
    for (let s = 0; s < segCount; s++) payload += b[i + 27 + s];
    // -1 marks a page whose packet does not complete here.
    if (g !== 0xffffffffffffffffn) {
      granule = Number(g);
      found = true;
    }
    i += 27 + segCount + payload;
  }
  if (!found) throw new MalformedError('no readable Ogg page');
  return { seconds: checkSeconds(granule / rate), format: 'ogg', exact: true };
}

// --- WebM / Matroska: Segment -> Info -------------------------------------

const ID_SEGMENT = 0x18538067;
const ID_INFO = 0x1549a966;
const ID_TIMECODE_SCALE = 0x2ad7b1;
const ID_DURATION = 0x4489;

function ebmlLen(first: number): number {
  for (let i = 0; i < 8; i++) {
    if (first & (0x80 >> i)) return i + 1;
  }
  return 0;
}

/**
 * Reads a class-A..D identifier, KEEPING its marker bits: spec ids are
 * written with them (Segment is 0x18538067, not 0x08538067), so
 * stripping would make every comparison miss.
 */
function ebmlId(b: Uint8Array, at: number): { id: number; len: number } | null {
  if (at >= b.length) return null;
  const n = ebmlLen(b[at]);
  if (n === 0 || n > 4 || at + n > b.length) return null;
  let id = 0;
  for (let i = 0; i < n; i++) id = id * 256 + b[at + i];
  return { id, len: n };
}

function ebmlSize(
  b: Uint8Array,
  at: number,
): { size: number; len: number; unknown: boolean } | null {
  if (at >= b.length) return null;
  const n = ebmlLen(b[at]);
  if (n === 0 || n > 8 || at + n > b.length) return null;
  let v = b[at] & (0xff >> n);
  let allOnes = v === (0xff >> n);
  for (let i = 1; i < n; i++) {
    v = v * 256 + b[at + i];
    if (b[at + i] !== 0xff) allOnes = false;
  }
  return { size: v, len: n, unknown: allOnes };
}

function ebmlFind(b: Uint8Array, want: number): Uint8Array | null {
  let pos = 0;
  while (pos < b.length) {
    const id = ebmlId(b, pos);
    if (!id) return null;
    const sz = ebmlSize(b, pos + id.len);
    if (!sz) return null;
    const body = pos + id.len + sz.len;
    if (body > b.length) return null;
    let end = b.length;
    if (!sz.unknown && body + sz.size <= b.length) end = body + sz.size;
    if (id.id === want) return b.subarray(body, end);
    if (end <= pos) return null;
    pos = end;
  }
  return null;
}

function probeMatroska(b: Uint8Array, _v: DataView): DurationResult {
  const seg = ebmlFind(b, ID_SEGMENT);
  if (!seg) throw new MalformedError('no Segment element');
  const info = ebmlFind(seg, ID_INFO);
  if (!info) throw new MalformedError('no Info element');

  let scaleNs = 1000000; // spec default: 1 ms per tick
  const rawScale = ebmlFind(info, ID_TIMECODE_SCALE);
  if (rawScale && rawScale.length > 0) {
    let n = 0;
    for (const c of rawScale) n = n * 256 + c;
    scaleNs = n;
  }
  const rawDur = ebmlFind(info, ID_DURATION);
  if (!rawDur) {
    // Live-recorded WebM often has no Duration until finalized.
    throw new MalformedError('Info carries no Duration');
  }
  const dv = new DataView(rawDur.buffer, rawDur.byteOffset, rawDur.byteLength);
  let ticks: number;
  if (rawDur.length === 4) ticks = dv.getFloat32(0);
  else if (rawDur.length === 8) ticks = dv.getFloat64(0);
  else throw new MalformedError('Duration is not a float');
  if (scaleNs === 0) throw new MalformedError('zero timecode scale');
  return { seconds: checkSeconds((ticks * scaleNs) / 1e9), format: 'webm', exact: true };
}

// --- MP3: exact only with a Xing/Info or VBRI frame count -----------------

function isMp3FrameSync(b: Uint8Array, at: number): boolean {
  return at + 1 < b.length && b[at] === 0xff && (b[at + 1] & 0xe0) === 0xe0;
}

const MP3_SAMPLE_RATES = [
  [11025, 12000, 8000, 0], // MPEG 2.5
  [0, 0, 0, 0], // reserved
  [22050, 24000, 16000, 0], // MPEG 2
  [44100, 48000, 32000, 0], // MPEG 1
];

function probeMp3(b: Uint8Array, _v: DataView): DurationResult {
  let audio = b;
  if (ascii(b, 0, 3) === 'ID3') {
    if (b.length < 10) throw new MalformedError('ID3 header truncated');
    // Syncsafe 28-bit size, 7 bits per byte.
    const size =
      ((b[6] & 0x7f) << 21) | ((b[7] & 0x7f) << 14) | ((b[8] & 0x7f) << 7) | (b[9] & 0x7f);
    const off = 10 + size;
    if (off >= b.length) throw new MalformedError('ID3 tag covers the whole upload');
    audio = b.subarray(off);
  }

  let start = -1;
  for (let i = 0; i + 4 <= audio.length && i < 1 << 16; i++) {
    if (isMp3FrameSync(audio, i)) {
      start = i;
      break;
    }
  }
  if (start < 0) throw new MalformedError('no MP3 frame sync');
  audio = audio.subarray(start);

  const version = (audio[1] >> 3) & 0x03;
  const layer = (audio[1] >> 1) & 0x03;
  if (version === 1 || layer === 0) throw new MalformedError('reserved MPEG version or layer');
  const sampleRate = MP3_SAMPLE_RATES[version][(audio[2] >> 2) & 0x03];
  if (sampleRate === 0) throw new MalformedError('reserved sample rate');
  const channelMode = (audio[3] >> 6) & 0x03;

  const frames = mp3FrameCount(audio, version, channelMode);
  if (frames !== null) {
    const samplesPerFrame = version === 3 ? 1152 : 576;
    return {
      seconds: checkSeconds((frames * samplesPerFrame) / sampleRate),
      format: 'mp3',
      exact: true,
    };
  }

  // No declared frame count. A constant-bitrate estimate is the best
  // available and is reported as INEXACT — the estimator refuses it, and
  // only the billing path may choose to accept one.
  const bitrateKbps = mp3Bitrate(version, (audio[2] >> 4) & 0x0f);
  if (bitrateKbps === 0) throw new MalformedError('free-format bitrate');
  return {
    seconds: checkSeconds((audio.length * 8) / (bitrateKbps * 1000)),
    format: 'mp3',
    exact: false,
  };
}

const MP3_BITRATE_V1_L3 = [0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 0];
const MP3_BITRATE_V2_L3 = [0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160, 0];

function mp3Bitrate(version: number, idx: number): number {
  return version === 3 ? MP3_BITRATE_V1_L3[idx] : MP3_BITRATE_V2_L3[idx];
}

function mp3FrameCount(frame: Uint8Array, version: number, channelMode: number): number | null {
  const dv = new DataView(frame.buffer, frame.byteOffset, frame.byteLength);
  if (frame.length >= 36 + 8 && ascii(frame, 36, 4) === 'VBRI') {
    return dv.getUint32(36 + 14);
  }
  let off: number;
  if (version === 3) off = channelMode === 3 ? 4 + 17 : 4 + 32;
  else off = channelMode === 3 ? 4 + 9 : 4 + 17;
  if (off + 8 > frame.length) return null;
  const tag = ascii(frame, off, 4);
  if (tag !== 'Xing' && tag !== 'Info') return null;
  const flags = dv.getUint32(off + 4);
  if ((flags & 0x1) === 0) return null; // no frame-count field
  if (off + 12 > frame.length) return null;
  return dv.getUint32(off + 8);
}
