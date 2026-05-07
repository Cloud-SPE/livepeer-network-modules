export interface ParsedRtmpKey {
  apiKey?: string;
  streamKey: string;
}

export function parsePublishingPath(path: string): ParsedRtmpKey {
  const trimmed = path.replace(/^\/+/, "").replace(/\/+$/, "");
  const parts = trimmed.split("/");
  if (parts.length === 0 || parts[0] === "") {
    return { streamKey: "" };
  }
  if (parts.length === 1) {
    return { streamKey: parts[0]! };
  }
  return { apiKey: parts[0]!, streamKey: parts[parts.length - 1]! };
}
