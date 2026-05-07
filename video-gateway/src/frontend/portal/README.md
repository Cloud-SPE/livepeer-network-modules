# `video-gateway/src/frontend/portal/`

Customer-facing portal SPA for the video product. pnpm sub-workspace
`@livepeer-rewrite/video-gateway-portal`.

Composes `customer-portal/frontend/shared/` widgets (signup / login /
api-keys / billing / account) and adds video-specific routes:

- `/portal/assets` — VOD library: list, tus upload, soft-delete (with
  confirm modal), restore.
- `/portal/streams` — live-stream creation: form → RTMP ingest URL +
  LL-HLS playback URL + session-key (one-time copy-to-clipboard);
  per-stream end action.
- `/portal/webhooks` — register URLs + signing-secret (one-time reveal
  + rotation) + delivery log + HMAC verification example snippet.
- `/portal/recordings` — `record_to_vod` toggle (default OFF) + list of
  recorded sessions linking back to assets.

Customer auth via `customer-portal`'s API-key flow (handled by Fastify
preHandler at the gateway level, not in the SPA).

## Build

```sh
pnpm -F @livepeer-rewrite/video-gateway-portal build
```

## Tests

```sh
pnpm -F @livepeer-rewrite/video-gateway-portal test
```
