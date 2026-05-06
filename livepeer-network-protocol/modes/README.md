# modes/

One spec per interaction mode. The initial six (resolution of plan 0002):

- [`http-reqresp.md`](./http-reqresp.md) — one HTTP req → one HTTP resp. **Accepted 2026-05-06.**
- [`http-stream.md`](./http-stream.md) — request → SSE / chunked-response stream. **Accepted 2026-05-06.**
- [`http-multipart.md`](./http-multipart.md) — multipart upload → JSON or binary response. **Drafted 2026-05-06; pending review.**
- [`ws-realtime.md`](./ws-realtime.md) — bidirectional WebSocket. **Drafted 2026-05-06; pending review.**
- [`rtmp-ingress-hls-egress.md`](./rtmp-ingress-hls-egress.md) — RTMP in → HLS manifest+segments out. **Drafted 2026-05-06; pending review.**
- [`session-control-plus-media.md`](./session-control-plus-media.md) — HTTP session-open → long-lived capability-defined media plane. **Drafted 2026-05-06; pending review.**

**Status:** all six initial modes drafted. `http-reqresp` and `http-stream` accepted;
remaining four pending review.

Each mode has its own SemVer (per Q2 hybrid SemVer). Mode files declare their version
in YAML frontmatter.

To propose a new mode after these six land, see [`../PROCESS.md`](../PROCESS.md).
