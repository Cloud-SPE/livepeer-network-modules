/**
 * Minimal multipart/form-data part extraction.
 *
 * Deliberately not a general parser: it finds one named file part in a
 * buffer already held in memory, which is the whole job here. A general
 * one is a much larger surface for something that has to agree
 * byte-for-byte with a Go implementation.
 */

export class MultipartError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'MultipartError';
  }
}

function boundaryOf(contentType: string): string | null {
  const m = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(contentType);
  if (!m) return null;
  return (m[1] ?? m[2]).trim();
}

/**
 * ASCII encode/decode, rather than TextEncoder/TextDecoder.
 *
 * Multipart boundaries and part headers are ASCII by RFC 2046, so the
 * platform text APIs buy nothing here and cost an ambient dependency —
 * they are DOM in one runtime and Node types in another. Keeping to
 * bytes lets this package run anywhere JS does, which matters because
 * the ceiling it computes is wanted wherever the upload is handled.
 */
function asciiBytes(s: string): Uint8Array {
  const out = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) out[i] = s.charCodeAt(i) & 0xff;
  return out;
}

function asciiString(b: Uint8Array): string {
  let out = '';
  for (let i = 0; i < b.length; i++) out += String.fromCharCode(b[i]);
  return out;
}

function indexOfBytes(hay: Uint8Array, needle: Uint8Array, from: number): number {
  outer: for (let i = from; i + needle.length <= hay.length; i++) {
    for (let j = 0; j < needle.length; j++) {
      if (hay[i + j] !== needle[j]) continue outer;
    }
    return i;
  }
  return -1;
}

/**
 * Returns the bytes of the named file part, or the whole body when it is
 * not multipart.
 */
export function extractFilePart(
  body: Uint8Array,
  contentType: string,
  fileField: string,
): Uint8Array {
  if (!body || body.length === 0) throw new MultipartError('empty request body');
  if (!/^multipart\//i.test(contentType.trim())) return body;

  const boundary = boundaryOf(contentType);
  if (!boundary) throw new MultipartError('multipart content-type has no boundary');

  const delim = asciiBytes(`--${boundary}`);
  const headerEnd = asciiBytes('\r\n\r\n');

  let pos = indexOfBytes(body, delim, 0);
  if (pos < 0) throw new MultipartError('no multipart boundary in body');

  while (pos >= 0) {
    const partStart = pos + delim.length;
    // "--" after the delimiter marks the final boundary.
    if (partStart + 2 <= body.length && body[partStart] === 0x2d && body[partStart + 1] === 0x2d) {
      break;
    }
    const hEnd = indexOfBytes(body, headerEnd, partStart);
    if (hEnd < 0) break;
    const headers = asciiString(body.subarray(partStart, hEnd));
    const next = indexOfBytes(body, delim, hEnd + headerEnd.length);
    const bodyStart = hEnd + headerEnd.length;
    // The CRLF before the next delimiter belongs to the delimiter.
    const bodyEnd = next < 0 ? body.length : Math.max(bodyStart, next - 2);

    const nameMatch = /name="([^"]*)"/i.exec(headers);
    if (nameMatch && nameMatch[1] === fileField) {
      return body.subarray(bodyStart, bodyEnd);
    }
    pos = next;
  }
  throw new MultipartError(`no ${JSON.stringify(fileField)} part in the upload`);
}
