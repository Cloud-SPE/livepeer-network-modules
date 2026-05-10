export function extractMultipartField(
  body: Buffer,
  contentType: string,
  fieldName: string,
): string | null {
  const boundary = parseBoundary(contentType);
  if (!boundary) return null;

  const delimiter = Buffer.from(`--${boundary}`, "utf8");
  let cursor = 0;
  while (cursor < body.length) {
    const start = body.indexOf(delimiter, cursor);
    if (start < 0) break;
    cursor = start + delimiter.length;

    if (body.subarray(cursor, cursor + 2).equals(Buffer.from("--", "utf8"))) {
      break;
    }
    if (body.subarray(cursor, cursor + 2).equals(Buffer.from("\r\n", "utf8"))) {
      cursor += 2;
    }

    const next = body.indexOf(delimiter, cursor);
    if (next < 0) break;
    const part = body.subarray(cursor, next);
    const headerEnd = part.indexOf(Buffer.from("\r\n\r\n", "utf8"));
    if (headerEnd < 0) {
      cursor = next;
      continue;
    }

    const headers = part.subarray(0, headerEnd).toString("utf8");
    if (!hasFieldName(headers, fieldName)) {
      cursor = next;
      continue;
    }

    const rawValue = part.subarray(headerEnd + 4);
    const value = stripTrailingCrlf(rawValue).toString("utf8").trim();
    return value.length > 0 ? value : null;
  }

  return null;
}

function parseBoundary(contentType: string): string | null {
  const match = /boundary=(?:"([^"]+)"|([^;]+))/i.exec(contentType);
  return (match?.[1] ?? match?.[2] ?? "").trim() || null;
}

function hasFieldName(headers: string, fieldName: string): boolean {
  const escaped = fieldName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return new RegExp(`name="${escaped}"`, "i").test(headers);
}

function stripTrailingCrlf(value: Buffer): Buffer {
  if (value.length >= 2 && value.subarray(value.length - 2).equals(Buffer.from("\r\n", "utf8"))) {
    return value.subarray(0, value.length - 2);
  }
  return value;
}
