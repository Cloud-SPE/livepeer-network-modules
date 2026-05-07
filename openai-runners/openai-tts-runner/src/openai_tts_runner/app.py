import asyncio
import io
import logging
import os
import subprocess
from contextlib import asynccontextmanager
from typing import Optional

import numpy as np
import torch
from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from pydantic import BaseModel, Field

from .gpu_probe import fail_fast_if_cuda_requested_without_gpu
from .kokoro_loader import KOKORO_SAMPLE_RATE, KokoroPipeline

MODEL_ID = os.environ.get("MODEL_ID", "hexgrad/Kokoro-82M")
MODEL_DIR = os.environ.get("MODEL_DIR", "/models")
RUNNER_PORT = int(os.environ.get("RUNNER_PORT", "8080"))
MAX_QUEUE_SIZE = int(os.environ.get("MAX_QUEUE_SIZE", "5"))
DEVICE = os.environ.get("DEVICE", "cuda")
LANG_CODE = os.environ.get("LANG_CODE", "a")
MAX_INPUT_CHARS = int(os.environ.get("MAX_INPUT_CHARS", "4000"))
DEFAULT_VOICE = os.environ.get("DEFAULT_VOICE", "af_bella")
METRICS_ENABLED = os.environ.get("METRICS_ENABLED", "false").lower() in ("true", "1", "yes")

CAPABILITY_NAME = os.environ.get("CAPABILITY_NAME", "openai-audio-speech")
CAP_SPEECH = "openai-audio-speech"
MODEL_ALIAS = "kokoro"

OPENAI_VOICE_MAP = {
    "alloy":   "af_bella",
    "echo":    "am_michael",
    "fable":   "bm_george",
    "onyx":    "am_adam",
    "nova":    "af_sarah",
    "shimmer": "af_bella",
    "ash":     "am_adam",
    "sage":    "bf_emma",
    "verse":   "am_michael",
    "coral":   "bf_isabella",
    "ballad":  "bm_lewis",
}

FORMAT_TABLE = {
    "mp3":  (["-f", "mp3",  "-b:a", "128k"],        "audio/mpeg"),
    "opus": (["-f", "opus", "-b:a", "64k"],         "audio/opus"),
    "aac":  (["-f", "adts", "-b:a", "128k"],        "audio/aac"),
    "flac": (["-f", "flac"],                         "audio/flac"),
    "wav":  ([],                                     "audio/wav"),
    "pcm":  ([],                                     f"audio/L16;rate={KOKORO_SAMPLE_RATE}"),
}

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("kokoro-runner")

_kokoro = KokoroPipeline()
_semaphore: Optional[asyncio.Semaphore] = None


def _resolve_voice(voice: Optional[str]) -> str:
    if not voice:
        return DEFAULT_VOICE
    if voice in OPENAI_VOICE_MAP:
        return OPENAI_VOICE_MAP[voice]
    return voice


def _encode_wav(samples: np.ndarray) -> bytes:
    import soundfile as sf

    buf = io.BytesIO()
    sf.write(buf, samples, KOKORO_SAMPLE_RATE, format="WAV", subtype="PCM_16")
    return buf.getvalue()


def _encode_pcm(samples: np.ndarray) -> bytes:
    clipped = np.clip(samples, -1.0, 1.0)
    as_int16 = (clipped * 32767.0).astype(np.int16)
    return as_int16.tobytes()


def _encode_with_ffmpeg(samples: np.ndarray, args: list) -> bytes:
    f32_bytes = samples.astype(np.float32).tobytes()
    cmd = [
        "ffmpeg", "-nostdin", "-loglevel", "error",
        "-f", "f32le", "-ac", "1", "-ar", str(KOKORO_SAMPLE_RATE),
        "-i", "pipe:0",
        *args,
        "pipe:1",
    ]
    proc = subprocess.run(cmd, input=f32_bytes, capture_output=True, check=False)
    if proc.returncode != 0:
        raise RuntimeError(
            f"ffmpeg encode failed: {proc.stderr.decode('utf-8', errors='replace')[:200]}"
        )
    return proc.stdout


def _encode(samples: np.ndarray, response_format: str) -> tuple[bytes, str]:
    if response_format not in FORMAT_TABLE:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": f"Unsupported response_format '{response_format}'. Supported: {sorted(FORMAT_TABLE)}", "type": "invalid_request_error"}},
        )
    args, mime = FORMAT_TABLE[response_format]
    if response_format == "wav":
        return _encode_wav(samples), mime
    if response_format == "pcm":
        return _encode_pcm(samples), mime
    return _encode_with_ffmpeg(samples, args), mime


class SpeechRequest(BaseModel):
    model: Optional[str] = None
    input: str = Field(..., min_length=1)
    voice: Optional[str] = None
    response_format: str = "mp3"
    speed: float = 1.0


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _semaphore

    fail_fast_if_cuda_requested_without_gpu(DEVICE)

    _kokoro.load(LANG_CODE, DEVICE, DEFAULT_VOICE)
    _semaphore = asyncio.Semaphore(MAX_QUEUE_SIZE)
    logger.info(f"Kokoro runner ready — model={MODEL_ID}, queue_size={MAX_QUEUE_SIZE}")
    yield
    logger.info("Shutting down, releasing GPU memory...")
    _kokoro.pipeline = None
    if DEVICE == "cuda":
        try:
            torch.cuda.empty_cache()
        except Exception:
            pass


app = FastAPI(title="openai-tts-runner", lifespan=lifespan)


@app.post("/v1/audio/speech")
async def speech(req: SpeechRequest):
    text = (req.input or "").strip()
    if not text:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": "input must be a non-empty string", "type": "invalid_request_error"}},
        )
    if len(text) > MAX_INPUT_CHARS:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": f"input exceeds {MAX_INPUT_CHARS} characters", "type": "invalid_request_error"}},
        )

    voice = _resolve_voice(req.voice)
    speed = req.speed if req.speed and req.speed > 0 else 1.0

    if _semaphore.locked() and _semaphore._value == 0:
        raise HTTPException(
            status_code=429,
            detail={"error": {"message": f"Server busy — max queue size ({MAX_QUEUE_SIZE}) reached. Try again later.", "type": "rate_limit_error"}},
        )

    async with _semaphore:
        loop = asyncio.get_event_loop()
        try:
            samples = await loop.run_in_executor(None, _kokoro.synthesize, text, voice, speed)
        except Exception as exc:
            logger.exception("Synthesis failed")
            raise HTTPException(
                status_code=500,
                detail={"error": {"message": f"Synthesis failed: {exc}", "type": "server_error"}},
            )

    try:
        audio_bytes, mime = _encode(samples, req.response_format)
    except HTTPException:
        raise
    except Exception as exc:
        logger.exception("Audio encoding failed")
        raise HTTPException(
            status_code=500,
            detail={"error": {"message": f"Audio encoding failed: {exc}", "type": "server_error"}},
        )

    return Response(content=audio_bytes, media_type=mime)


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "model": MODEL_ID, "device": DEVICE}


@app.get(f"/{CAP_SPEECH}/options")
async def speech_options():
    return {"models": [MODEL_ALIAS]}


if METRICS_ENABLED:
    from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

    @app.get("/metrics")
    async def metrics():
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)
