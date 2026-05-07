# mock-runner

Tiny FastAPI shim returning canned responses for the five paid OpenAI
endpoints (`/v1/chat/completions`, `/v1/embeddings`,
`/v1/audio/transcriptions`, `/v1/audio/speech`,
`/v1/images/generations`). Lets `make smoke` exercise the full
`openai-gateway -> capability-broker -> runner` chain offline (no GPU,
no real vLLM / Whisper / Diffusers / kokoro-tts).

This image is the only Python surface in the openai-gateway deployment
artefacts (plan 0013-openai OQ1). Tag tracks the gateway image.

## Build

```sh
docker build -t openai-gateway-mock-runner:dev .
```

## Run standalone

```sh
docker run --rm -p 8081:8081 openai-gateway-mock-runner:dev
curl -sS -X POST http://localhost:8081/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"mock-chat","messages":[{"role":"user","content":"hi"}]}'
```
