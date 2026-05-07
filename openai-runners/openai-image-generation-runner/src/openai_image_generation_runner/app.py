import asyncio
import base64
import io
import logging
import os
import time
from contextlib import asynccontextmanager
from typing import Optional

import torch
from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from pydantic import BaseModel, Field

from .diffusers_loader import (
    DTYPE_MAP,
    DiffusersPipeline,
    get_default_guidance,
    get_default_steps,
)
from .gpu_probe import fail_fast_if_cuda_requested_without_gpu

MODEL_ID = os.environ.get("MODEL_ID", "")
CAPABILITY_NAME = os.environ.get("CAPABILITY_NAME", "image-generation")
MODEL_DIR = os.environ.get("MODEL_DIR", "/models")
RUNNER_PORT = int(os.environ.get("RUNNER_PORT", "8080"))
MAX_QUEUE_SIZE = int(os.environ.get("MAX_QUEUE_SIZE", "5"))
DEVICE = os.environ.get("DEVICE", "cuda")
DTYPE_STR = os.environ.get("DTYPE", "float16")
USE_TORCH_COMPILE = os.environ.get("USE_TORCH_COMPILE", "false").lower() in ("true", "1", "yes")
DEFAULT_WIDTH = int(os.environ.get("DEFAULT_WIDTH", "1024"))
DEFAULT_HEIGHT = int(os.environ.get("DEFAULT_HEIGHT", "1024"))
DEFAULT_STEPS = os.environ.get("DEFAULT_STEPS", "")
DEFAULT_GUIDANCE = os.environ.get("DEFAULT_GUIDANCE", "")
METRICS_ENABLED = os.environ.get("METRICS_ENABLED", "false").lower() in ("true", "1", "yes")

if DTYPE_STR.lower() not in DTYPE_MAP:
    logging.warning(
        f"DTYPE='{DTYPE_STR}' is not supported (supported: {list(DTYPE_MAP)}). "
        f"Falling back to bfloat16."
    )
    DTYPE_STR = "bfloat16"

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("image-runner")

_diffusers = DiffusersPipeline()
_semaphore: Optional[asyncio.Semaphore] = None


class ImageGenerationRequest(BaseModel):
    model: Optional[str] = None
    prompt: str
    n: int = Field(default=1, ge=1, le=10)
    size: Optional[str] = None
    response_format: Optional[str] = "b64_json"
    quality: Optional[str] = None
    num_inference_steps: Optional[int] = None
    guidance_scale: Optional[float] = None
    negative_prompt: Optional[str] = None
    seed: Optional[int] = None


class ImageData(BaseModel):
    b64_json: Optional[str] = None
    url: Optional[str] = None
    revised_prompt: Optional[str] = None


class ImageGenerationResponse(BaseModel):
    created: int
    data: list[ImageData]


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _semaphore

    if not MODEL_ID:
        logger.error("MODEL_ID environment variable is required")
        raise RuntimeError("MODEL_ID is required")

    fail_fast_if_cuda_requested_without_gpu(DEVICE)

    dtype = DTYPE_MAP.get(DTYPE_STR.lower(), torch.float16)
    _diffusers.load(MODEL_ID, MODEL_DIR, DEVICE, dtype, USE_TORCH_COMPILE)

    _semaphore = asyncio.Semaphore(MAX_QUEUE_SIZE)

    logger.info(f"Image runner ready — model={MODEL_ID}, queue_size={MAX_QUEUE_SIZE}")
    yield

    logger.info("Shutting down, releasing GPU memory...")
    _diffusers.pipeline = None
    if DEVICE == "cuda":
        torch.cuda.empty_cache()


app = FastAPI(title="openai-image-generation-runner", lifespan=lifespan)


def _parse_size(size: Optional[str]) -> tuple[int, int]:
    if not size:
        return DEFAULT_WIDTH, DEFAULT_HEIGHT
    try:
        parts = size.lower().split("x")
        return int(parts[0]), int(parts[1])
    except (ValueError, IndexError):
        return DEFAULT_WIDTH, DEFAULT_HEIGHT


def _generate_images_sync(req: ImageGenerationRequest) -> list[bytes]:
    width, height = _parse_size(req.size)
    steps = req.num_inference_steps or get_default_steps(MODEL_ID, DEFAULT_STEPS)
    guidance = (
        req.guidance_scale
        if req.guidance_scale is not None
        else get_default_guidance(MODEL_ID, DEFAULT_GUIDANCE)
    )

    if req.quality == "hd":
        steps = max(steps, steps * 2)

    kwargs = {
        "prompt": req.prompt,
        "width": width,
        "height": height,
        "num_inference_steps": steps,
        "guidance_scale": guidance,
        "num_images_per_prompt": 1,
    }

    if req.negative_prompt and _diffusers.model_family in ("RealVisXL", "SDXL"):
        kwargs["negative_prompt"] = req.negative_prompt

    logger.info(
        f"Generating {req.n} image(s): {width}x{height}, steps={steps}, "
        f"guidance={guidance}, seed={req.seed}"
    )

    start = time.time()
    png_buffers = []
    with _diffusers.lock, torch.inference_mode():
        for i in range(req.n):
            if req.seed is not None:
                gen = torch.Generator(device=DEVICE).manual_seed(req.seed + i)
            else:
                gen = None
            result = _diffusers.pipeline(**kwargs, generator=gen)
            buf = io.BytesIO()
            result.images[0].save(buf, format="PNG")
            png_buffers.append(buf.getvalue())

    elapsed = time.time() - start
    logger.info(f"Generation complete in {elapsed:.1f}s ({req.n} images)")

    return png_buffers


@app.post("/v1/images/generations", response_model=ImageGenerationResponse)
async def create_image(req: ImageGenerationRequest):
    if _semaphore.locked() and _semaphore._value == 0:
        raise HTTPException(
            status_code=429,
            detail={"error": {"message": f"Server busy — max queue size ({MAX_QUEUE_SIZE}) reached. Try again later.", "type": "rate_limit_error"}},
        )

    async with _semaphore:
        loop = asyncio.get_event_loop()
        try:
            png_buffers = await loop.run_in_executor(None, _generate_images_sync, req)
        except torch.cuda.OutOfMemoryError:
            torch.cuda.empty_cache()
            raise HTTPException(
                status_code=507,
                detail={"error": {"message": "GPU out of memory. Try a smaller size or fewer images.", "type": "server_error"}},
            )
        except Exception as exc:
            logger.exception("Image generation failed")
            raise HTTPException(
                status_code=500,
                detail={"error": {"message": f"Generation failed: {exc}", "type": "server_error"}},
            )

    data = []
    for png_bytes in png_buffers:
        b64 = base64.b64encode(png_bytes).decode("utf-8")
        data.append(ImageData(b64_json=b64))

    return ImageGenerationResponse(created=int(time.time()), data=data)


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "model": MODEL_ID, "device": DEVICE}


async def _options():
    return {"models": [MODEL_ID]}


app.add_api_route("/options", _options, methods=["GET"])
app.add_api_route(f"/{CAPABILITY_NAME}/options", _options, methods=["GET"])


if METRICS_ENABLED:
    from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

    @app.get("/metrics")
    async def metrics():
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)
