# openai-tester

Node.js integration test harness that exercises every openai-runner
through the OpenAI SDK. One test script per capability.

## Test scripts

| Script | Capability |
|---|---|
| `generate-test-audio.sh` | Generate a local spoken audio fixture for transcription tests |
| `test-gateway-chat.mjs` | OpenAI SDK smoke test against an OpenAI-compatible gateway |
| `test-chat-completion.mjs` | `openai-chat-completions` |
| `test-text-embedding.mjs` | `openai-text-embeddings` |
| `test-audio-transcription.mjs` | `openai-audio-transcriptions` |
| `test-audio-translation.mjs` | `openai-audio-translations` |
| `test-audio-speech.mjs` | `openai-audio-speech` |
| `test-image-generation.mjs` | `image-generation` |

## Configuration

| Env var | Default | Purpose |
|---|---|---|
| `OPENAI_BASE_URL` | `http://localhost:8090/v1` | Runner endpoint |
| `OPENAI_API_KEY` | `local-dev-no-auth` | Bearer token (runners ignore) |
| `MODEL` | varies per script | Model alias |

## Run

```bash
npm install
node test-chat-completion.mjs

./generate-test-audio.sh test.ogg
OPENAI_BASE_URL=http://localhost:8090/v1 \
OPENAI_API_KEY=local-dev-no-auth \
MODEL=whisper-large-v3 \
AUDIO_FILE=test.ogg \
node test-audio-transcription.mjs

OPENAI_BASE_URL=https://openai-gw-sea.cloudspe.com/v1 \
OPENAI_API_KEY=sk-live-... \
MODEL=gpt-4.1-mini \
node test-gateway-chat.mjs

# or via Docker:
docker run --rm \
  -e OPENAI_BASE_URL=http://broker:8090/v1 \
  tztcloud/openai-tester:v0.8.10 \
  node test-chat-completion.mjs
```

## Source attribution

Ported verbatim from `livepeer-byoc/openai-runners/openai-tester/`.
The `package.json` `name` field changed from
`byoc-openai-runner-tester` to `openai-runner-tester` (per
user-memory `feedback_no_byoc_term.md`); test scripts unchanged.
