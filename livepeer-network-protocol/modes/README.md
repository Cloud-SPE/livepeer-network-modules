# modes/

One spec per interaction mode. The initial six (resolution of plan 0002):

- `http-reqresp.md` — one HTTP req → one HTTP resp.
- `http-stream.md` — request → SSE / chunked-response stream.
- `http-multipart.md` — multipart upload → JSON or binary response.
- `ws-realtime.md` — bidirectional WebSocket.
- `rtmp-ingress-hls-egress.md` — RTMP in → HLS manifest+segments out.
- `session-control-plus-media.md` — HTTP session-open → long-lived media plane.

**Status:** specs TBD per [plan 0002](../../docs/exec-plans/active/0002-define-interaction-modes-spec.md).

Each mode has its own SemVer (per Q2 hybrid SemVer). Mode files declare their version
in YAML frontmatter.

To propose a new mode after these six land, see [`../PROCESS.md`](../PROCESS.md).
