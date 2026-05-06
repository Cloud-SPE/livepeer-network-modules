# modes/

One spec per interaction mode. The initial six (resolution of plan 0002):

- [`http-reqresp.md`](./http-reqresp.md) — one HTTP req → one HTTP resp. **Accepted 2026-05-06.**
- [`http-stream.md`](./http-stream.md) — request → SSE / chunked-response stream. **Accepted 2026-05-06.**
- [`http-multipart.md`](./http-multipart.md) — multipart upload → JSON or binary response. **Accepted 2026-05-06.**
- [`ws-realtime.md`](./ws-realtime.md) — bidirectional WebSocket. **Accepted 2026-05-06.**
- [`rtmp-ingress-hls-egress.md`](./rtmp-ingress-hls-egress.md) — RTMP in → HLS manifest+segments out. **Accepted 2026-05-06.**
- [`session-control-plus-media.md`](./session-control-plus-media.md) — HTTP session-open → long-lived capability-defined media plane. **Accepted 2026-05-06.**

**Status:** all six initial modes accepted. To propose a seventh, see
[`../PROCESS.md`](../PROCESS.md).

Each mode has its own SemVer (per Q2 hybrid SemVer). Mode files declare their version
in YAML frontmatter.

To propose a new mode after these six land, see [`../PROCESS.md`](../PROCESS.md).
