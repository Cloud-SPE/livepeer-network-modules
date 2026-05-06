# modes/

One spec per interaction mode. The initial six (resolution of plan 0002):

- [`http-reqresp.md`](./http-reqresp.md) — one HTTP req → one HTTP resp. **Accepted 2026-05-06.**
- [`http-stream.md`](./http-stream.md) — request → SSE / chunked-response stream. **Drafted 2026-05-06; pending review.**
- `http-multipart.md` — multipart upload → JSON or binary response. *(stub)*
- `ws-realtime.md` — bidirectional WebSocket. *(stub)*
- `rtmp-ingress-hls-egress.md` — RTMP in → HLS manifest+segments out. *(stub)*
- `session-control-plus-media.md` — HTTP session-open → long-lived media plane. *(stub)*

**Status:** `http-reqresp` accepted as the template. `http-stream` drafted as a delta
from it. Remaining four modes follow.

Each mode has its own SemVer (per Q2 hybrid SemVer). Mode files declare their version
in YAML frontmatter.

To propose a new mode after these six land, see [`../PROCESS.md`](../PROCESS.md).
