import asyncio
import io
import logging
import os
import subprocess
from contextlib import asynccontextmanager
from typing import Optional

import numpy as np
import torch
from fastapi import FastAPI, File, Form, HTTPException, UploadFile
from fastapi.responses import JSONResponse, PlainTextResponse, Response

from .gpu_probe import fail_fast_if_cuda_requested_without_gpu
from .whisper_loader import DTYPE_MAP, TARGET_SAMPLE_RATE, WhisperPipeline

MODEL_ID = os.environ.get("MODEL_ID", "openai/whisper-large-v3")
MODEL_DIR = os.environ.get("MODEL_DIR", "/models")
RUNNER_PORT = int(os.environ.get("RUNNER_PORT", "8080"))
MAX_QUEUE_SIZE = int(os.environ.get("MAX_QUEUE_SIZE", "5"))
DEVICE = os.environ.get("DEVICE", "cuda")
DTYPE_STR = os.environ.get("DTYPE", "bfloat16")
MAX_AUDIO_MB = int(os.environ.get("MAX_AUDIO_MB", "50"))
CHUNK_LENGTH_S = int(os.environ.get("CHUNK_LENGTH_S", "30"))
INFERENCE_BATCH_SIZE = int(os.environ.get("INFERENCE_BATCH_SIZE", "16"))
METRICS_ENABLED = os.environ.get("METRICS_ENABLED", "false").lower() in ("true", "1", "yes")

CAPABILITY_NAME = os.environ.get("CAPABILITY_NAME", "openai-audio-transcriptions")
CAP_TRANSCRIPTIONS = "openai-audio-transcriptions"
CAP_TRANSLATIONS = "openai-audio-translations"
MODEL_ALIAS = "whisper-large-v3"

SUPPORTED_RESPONSE_FORMATS = {"json", "text", "srt", "vtt", "verbose_json"}

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("whisper-runner")

_whisper = WhisperPipeline()
_semaphore: Optional[asyncio.Semaphore] = None


def _decode_with_ffmpeg(data: bytes) -> np.ndarray:
    proc = subprocess.run(
        [
            "ffmpeg", "-nostdin", "-loglevel", "error",
            "-i", "pipe:0",
            "-f", "f32le", "-acodec", "pcm_f32le",
            "-ac", "1", "-ar", str(TARGET_SAMPLE_RATE),
            "pipe:1",
        ],
        input=data,
        capture_output=True,
        check=False,
    )
    if proc.returncode != 0:
        raise ValueError(
            f"ffmpeg decode failed: {proc.stderr.decode('utf-8', errors='replace')[:200]}"
        )
    return np.frombuffer(proc.stdout, dtype=np.float32).copy()


def _decode_audio(data: bytes) -> np.ndarray:
    import soundfile as sf

    try:
        audio, sr = sf.read(io.BytesIO(data), dtype="float32", always_2d=False)
    except Exception:
        return _decode_with_ffmpeg(data)

    if audio.ndim > 1:
        audio = audio.mean(axis=1)
    if sr != TARGET_SAMPLE_RATE:
        import torchaudio

        tensor = torch.from_numpy(audio.astype(np.float32)).unsqueeze(0)
        resampled = torchaudio.functional.resample(
            tensor, orig_freq=sr, new_freq=TARGET_SAMPLE_RATE
        )
        audio = resampled.squeeze(0).numpy()
    return audio.astype(np.float32, copy=False)


def _format_srt_timestamp(seconds: float) -> str:
    if seconds is None or seconds < 0:
        seconds = 0.0
    ms = int(round(seconds * 1000))
    h, ms = divmod(ms, 3_600_000)
    m, ms = divmod(ms, 60_000)
    s, ms = divmod(ms, 1000)
    return f"{h:02d}:{m:02d}:{s:02d},{ms:03d}"


def _format_vtt_timestamp(seconds: float) -> str:
    return _format_srt_timestamp(seconds).replace(",", ".")


def _chunks_to_segments(chunks):
    segments = []
    for i, ch in enumerate(chunks or []):
        ts = ch.get("timestamp") or (None, None)
        start = ts[0] if isinstance(ts, (list, tuple)) and len(ts) > 0 else None
        end = ts[1] if isinstance(ts, (list, tuple)) and len(ts) > 1 else None
        segments.append({
            "id": i,
            "start": float(start) if start is not None else 0.0,
            "end": float(end) if end is not None else 0.0,
            "text": (ch.get("text") or "").strip(),
        })
    return segments


def _render_srt(segments) -> str:
    lines = []
    for i, seg in enumerate(segments, start=1):
        lines.append(str(i))
        lines.append(f"{_format_srt_timestamp(seg['start'])} --> {_format_srt_timestamp(seg['end'])}")
        lines.append(seg["text"])
        lines.append("")
    return "\n".join(lines)


def _render_vtt(segments) -> str:
    lines = ["WEBVTT", ""]
    for seg in segments:
        lines.append(f"{_format_vtt_timestamp(seg['start'])} --> {_format_vtt_timestamp(seg['end'])}")
        lines.append(seg["text"])
        lines.append("")
    return "\n".join(lines)


def _build_response(result: dict, response_format: str, task: str, language: Optional[str], duration: float) -> Response:
    text = (result.get("text") or "").strip()
    segments = _chunks_to_segments(result.get("chunks"))

    if response_format == "text":
        return PlainTextResponse(text)
    if response_format == "srt":
        return PlainTextResponse(_render_srt(segments), media_type="application/x-subrip")
    if response_format == "vtt":
        return PlainTextResponse(_render_vtt(segments), media_type="text/vtt")
    if response_format == "verbose_json":
        return JSONResponse({
            "task": task,
            "language": language or "",
            "duration": duration,
            "text": text,
            "segments": segments,
        })
    return JSONResponse({"text": text})


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _semaphore

    fail_fast_if_cuda_requested_without_gpu(DEVICE)

    dtype = DTYPE_MAP.get(DTYPE_STR.lower(), torch.bfloat16)
    _whisper.load(MODEL_ID, MODEL_DIR, DEVICE, dtype, CHUNK_LENGTH_S, INFERENCE_BATCH_SIZE)

    _semaphore = asyncio.Semaphore(MAX_QUEUE_SIZE)

    logger.info(f"Whisper runner ready — model={MODEL_ID}, queue_size={MAX_QUEUE_SIZE}")
    yield

    logger.info("Shutting down, releasing GPU memory...")
    _whisper.pipeline = None
    if DEVICE == "cuda":
        torch.cuda.empty_cache()


app = FastAPI(title="openai-audio-runner", lifespan=lifespan)


async def _read_upload(file: UploadFile) -> bytes:
    data = await file.read()
    if len(data) > MAX_AUDIO_MB * 1024 * 1024:
        raise HTTPException(
            status_code=413,
            detail={"error": {"message": f"Audio file exceeds {MAX_AUDIO_MB} MB limit", "type": "invalid_request_error"}},
        )
    if not data:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": "file is empty", "type": "invalid_request_error"}},
        )
    return data


async def _handle_audio(
    task: str,
    file: UploadFile,
    language: Optional[str],
    prompt: Optional[str],
    response_format: str,
    temperature: float,
) -> Response:
    if response_format not in SUPPORTED_RESPONSE_FORMATS:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": f"Unsupported response_format '{response_format}'. Supported: {sorted(SUPPORTED_RESPONSE_FORMATS)}", "type": "invalid_request_error"}},
        )

    data = await _read_upload(file)

    try:
        audio = _decode_audio(data)
    except Exception as exc:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": f"Could not decode audio: {exc}", "type": "invalid_request_error"}},
        )

    duration = float(len(audio)) / TARGET_SAMPLE_RATE

    if _semaphore.locked() and _semaphore._value == 0:
        raise HTTPException(
            status_code=429,
            detail={"error": {"message": f"Server busy — max queue size ({MAX_QUEUE_SIZE}) reached. Try again later.", "type": "rate_limit_error"}},
        )

    async with _semaphore:
        loop = asyncio.get_event_loop()
        try:
            result = await loop.run_in_executor(
                None, _whisper.run, audio, task, language, prompt, temperature
            )
        except torch.cuda.OutOfMemoryError:
            torch.cuda.empty_cache()
            raise HTTPException(
                status_code=507,
                detail={"error": {"message": "GPU out of memory. Try shorter audio.", "type": "server_error"}},
            )
        except Exception as exc:
            logger.exception("Inference failed")
            raise HTTPException(
                status_code=500,
                detail={"error": {"message": f"Inference failed: {exc}", "type": "server_error"}},
            )

    effective_language = language if task == "transcribe" else "english"
    return _build_response(result, response_format, task, effective_language, duration)


@app.post("/v1/audio/transcriptions")
async def transcriptions(
    file: UploadFile = File(...),
    model: str = Form(...),
    language: Optional[str] = Form(None),
    prompt: Optional[str] = Form(None),
    response_format: str = Form("json"),
    temperature: float = Form(0.0),
):
    del model
    return await _handle_audio("transcribe", file, language, prompt, response_format, temperature)


@app.post("/v1/audio/translations")
async def translations(
    file: UploadFile = File(...),
    model: str = Form(...),
    prompt: Optional[str] = Form(None),
    response_format: str = Form("json"),
    temperature: float = Form(0.0),
):
    del model
    return await _handle_audio("translate", file, None, prompt, response_format, temperature)


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "model": MODEL_ID, "device": DEVICE}


@app.get(f"/{CAP_TRANSCRIPTIONS}/options")
async def transcriptions_options():
    return {"models": [MODEL_ALIAS]}


@app.get(f"/{CAP_TRANSLATIONS}/options")
async def translations_options():
    return {"models": [MODEL_ALIAS]}


if METRICS_ENABLED:
    from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

    @app.get("/metrics")
    async def metrics():
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)
