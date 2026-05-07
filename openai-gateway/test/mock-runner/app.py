"""Tiny mock runner for offline `make smoke` of the openai-gateway compose stack.

Returns canned vLLM- / Whisper- / kokoro-tts- / diffusers-shaped responses for
the five paid OpenAI endpoints the gateway forwards. Dispatched by the
gateway -> capability-broker chain off the `Livepeer-Capability` header.

This image is the lone Python surface in `openai-gateway/`'s deployment
artefacts (plan 0013-openai OQ1 lock); the gateway runtime itself stays
Node-only.
"""

from __future__ import annotations

import base64
import json
import time
from typing import Any

from fastapi import FastAPI, Request, Response
from fastapi.responses import JSONResponse, StreamingResponse

app = FastAPI(title="openai-gateway mock-runner")


@app.get("/healthz")
def healthz() -> Response:
    return Response(content="ok\n", media_type="text/plain")


@app.get("/options")
def options() -> JSONResponse:
    return JSONResponse(
        {
            "capabilities": [
                "openai:/v1/chat/completions",
                "openai:/v1/embeddings",
                "openai:/v1/audio/transcriptions",
                "openai:/v1/audio/speech",
                "openai:/v1/images/generations",
            ]
        }
    )


@app.post("/v1/chat/completions")
async def chat_completions(request: Request) -> Response:
    body = await _read_json(request)
    if body.get("stream") is True:
        return StreamingResponse(_stream_chat(body), media_type="text/event-stream")
    return JSONResponse(_canned_chat_response(body))


@app.post("/v1/embeddings")
async def embeddings(request: Request) -> Response:
    body = await _read_json(request)
    inputs = body.get("input") or [""]
    if isinstance(inputs, str):
        inputs = [inputs]
    return JSONResponse(
        {
            "object": "list",
            "data": [
                {
                    "object": "embedding",
                    "index": i,
                    "embedding": [0.0] * 8,
                }
                for i, _ in enumerate(inputs)
            ],
            "model": body.get("model", "mock-embeddings"),
            "usage": {"prompt_tokens": 5 * len(inputs), "total_tokens": 5 * len(inputs)},
        }
    )


@app.post("/v1/audio/transcriptions")
async def audio_transcriptions() -> Response:
    return JSONResponse({"text": "this is a mock transcription"})


@app.post("/v1/audio/speech")
async def audio_speech(request: Request) -> Response:
    # Until http-binary-stream@v0 lands, this endpoint will be reached only
    # when the gateway is run with OPENAI_AUDIO_SPEECH_ENABLED=true. Returns
    # a 1-byte placeholder so the smoke surface is non-empty.
    body = await _read_json(request)
    payload = b"\x00"
    return Response(
        content=payload,
        media_type="audio/wav",
        headers={"X-Mock-Voice": str(body.get("voice", "alloy"))},
    )


@app.post("/v1/images/generations")
async def images_generations(request: Request) -> Response:
    body = await _read_json(request)
    n = int(body.get("n", 1))
    return JSONResponse(
        {
            "created": int(time.time()),
            "data": [
                {
                    "b64_json": base64.b64encode(b"\x89PNG\r\n\x1a\n").decode("ascii"),
                }
                for _ in range(max(1, n))
            ],
        }
    )


def _canned_chat_response(body: dict[str, Any]) -> dict[str, Any]:
    return {
        "id": "chatcmpl-mock-1",
        "object": "chat.completion",
        "created": int(time.time()),
        "model": body.get("model", "mock-chat"),
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": "mock response"},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
    }


def _stream_chat(body: dict[str, Any]) -> Any:
    chunks = [
        {
            "id": "chatcmpl-mock-1",
            "object": "chat.completion.chunk",
            "created": int(time.time()),
            "model": body.get("model", "mock-chat"),
            "choices": [{"index": 0, "delta": {"role": "assistant"}, "finish_reason": None}],
        },
        {
            "id": "chatcmpl-mock-1",
            "object": "chat.completion.chunk",
            "created": int(time.time()),
            "model": body.get("model", "mock-chat"),
            "choices": [{"index": 0, "delta": {"content": "mock"}, "finish_reason": None}],
        },
        {
            "id": "chatcmpl-mock-1",
            "object": "chat.completion.chunk",
            "created": int(time.time()),
            "model": body.get("model", "mock-chat"),
            "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
        },
    ]
    for chunk in chunks:
        yield f"data: {json.dumps(chunk)}\n\n".encode("utf-8")
    yield b"data: [DONE]\n\n"


async def _read_json(request: Request) -> dict[str, Any]:
    raw = await request.body()
    if not raw:
        return {}
    try:
        return json.loads(raw.decode("utf-8"))
    except (UnicodeDecodeError, json.JSONDecodeError):
        return {}
