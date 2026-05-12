# OpenCode With openai-gateway

Date: 2026-05-12

This note records a working OpenCode setup against the deployed
`openai-gateway` endpoint at `https://openai-gw-sea.cloudspe.com/v1`.
It is point-in-time operator guidance based on a successful manual test.

## Outcome

OpenCode worked against `openai-gateway` after storing the API key via
OpenCode's `/connect` flow and removing `apiKey` from the custom provider
config.

The same gateway and key also worked in a standalone OpenAI SDK smoke test.

## Install

OpenCode was installed with:

```bash
npm install -g opencode-ai
```

## File locations

OpenCode configuration and credentials live in separate paths:

- Config: `~/.config/opencode/opencode.json`
- Auth credentials: `~/.local/share/opencode/auth.json`

This split matters for `openai-gateway` users:

- provider definition belongs in `opencode.json`
- gateway API key can be stored via `/connect`, which writes `auth.json`

## Working OpenCode config

Use a custom provider config like:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "blueclaw": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "BlueClaw Network",
      "options": {
        "baseURL": "https://openai-gw-sea.cloudspe.com/v1"
      },
      "models": {
        "Qwen3.6-27B": {
          "name": "Qwen3.6-27B",
          "cost": {
            "input": 0.4,
            "output": 1.2
          }
        }
      }
    }
  }
}
```

Important details:

- Provider id is `blueclaw`.
- Selected model should be `blueclaw/Qwen3.6-27B`.
- `apiKey` is omitted from config when using `/connect`.
- The `cost` block above matches the live `openai-gateway` admin rate card for
  `Qwen3.6-27B` at the time of this note.

## Working auth file shape

When credentials are stored in OpenCode's auth store, the file shape is:

```json
{
  "blueclaw": {
    "type": "api",
    "key": "YOUR_API_KEY"
  }
}
```

That content lives at:

```text
~/.local/share/opencode/auth.json
```

## Working auth flow

From inside OpenCode:

1. Run `/connect`.
2. Choose `Other`.
3. Enter provider id `blueclaw`.
4. Paste the gateway-issued API key (`sk-live-...`).

After that, requests to `POST /v1/chat/completions` succeeded.

An equivalent stored credential entry in `auth.json` is:

```json
{
  "blueclaw": {
    "type": "api",
    "key": "YOUR_API_KEY"
  }
}
```
