import logging
import os
import threading
import time
from typing import Optional

import torch

logger = logging.getLogger("image-runner")

DTYPE_MAP = {
    "float16": torch.float16,
    "fp16": torch.float16,
    "bfloat16": torch.bfloat16,
    "bf16": torch.bfloat16,
    "float32": torch.float32,
    "fp32": torch.float32,
}

MODEL_PROFILES = {
    "FLUX": {
        "default_steps": 28,
        "default_guidance": 3.5,
        "scheduler": None,
    },
    "RealVisXL": {
        "default_steps": 6,
        "default_guidance": 1.5,
        "scheduler": None,
    },
    "SDXL": {
        "default_steps": 20,
        "default_guidance": 7.5,
        "scheduler": None,
    },
}


def detect_model_family(model_id: str) -> str:
    lower = model_id.lower()
    if "flux" in lower:
        return "FLUX"
    if "realvis" in lower or "lightning" in lower:
        return "RealVisXL"
    if "sdxl" in lower or "stable-diffusion-xl" in lower:
        return "SDXL"
    return "SDXL"


def get_default_steps(model_id: str, override: str = "") -> int:
    if override:
        return int(override)
    family = detect_model_family(model_id)
    return MODEL_PROFILES.get(family, MODEL_PROFILES["SDXL"])["default_steps"]


def get_default_guidance(model_id: str, override: str = "") -> float:
    if override:
        return float(override)
    family = detect_model_family(model_id)
    return MODEL_PROFILES.get(family, MODEL_PROFILES["SDXL"])["default_guidance"]


class DiffusersPipeline:
    def __init__(self) -> None:
        self.pipeline: Optional[object] = None
        self.model_family: Optional[str] = None
        self.lock = threading.Lock()

    def load(
        self,
        model_id: str,
        model_dir: str,
        device: str,
        dtype: torch.dtype,
        use_torch_compile: bool,
    ) -> None:
        self.model_family = detect_model_family(model_id)
        logger.info(
            f"Loading model: {model_id} (family={self.model_family}, dtype={dtype}, device={device})"
        )
        logger.info(f"Model cache directory: {model_dir}")

        start = time.time()

        if self.model_family == "FLUX":
            from diffusers import FluxPipeline

            self.pipeline = FluxPipeline.from_pretrained(
                model_id,
                torch_dtype=dtype,
                cache_dir=model_dir,
            )
            self.pipeline.enable_model_cpu_offload()

        elif self.model_family in ("RealVisXL", "SDXL"):
            from diffusers import StableDiffusionXLPipeline

            self.pipeline = StableDiffusionXLPipeline.from_pretrained(
                model_id,
                torch_dtype=dtype,
                cache_dir=model_dir,
                use_safetensors=True,
            )
            self.pipeline.to(device)

        else:
            from diffusers import AutoPipelineForText2Image

            self.pipeline = AutoPipelineForText2Image.from_pretrained(
                model_id,
                torch_dtype=dtype,
                cache_dir=model_dir,
            )
            self.pipeline.to(device)

        if self.model_family != "FLUX":
            try:
                self.pipeline.enable_xformers_memory_efficient_attention()
                logger.info("xformers memory-efficient attention enabled")
            except Exception:
                logger.info("xformers not available, using default attention")
        else:
            logger.info("Skipping xformers for FLUX (uses built-in efficient attention)")

        if hasattr(self.pipeline, "vae") and hasattr(self.pipeline.vae, "enable_slicing"):
            self.pipeline.vae.enable_slicing()
            logger.info("VAE slicing enabled")

        if hasattr(self.pipeline, "vae") and hasattr(self.pipeline.vae, "enable_tiling"):
            self.pipeline.vae.enable_tiling()
            logger.info("VAE tiling enabled")

        triton_cache = os.environ.get("TRITON_CACHE_DIR", "")
        if triton_cache and os.path.isdir(triton_cache) and os.listdir(triton_cache):
            logger.info(f"Triton kernel cache found at {triton_cache} — reusing compiled kernels")
        elif triton_cache:
            logger.info(f"Triton kernel cache empty at {triton_cache} — first run will compile kernels")

        compile_mode = os.environ.get("TORCH_COMPILE_MODE", "default")
        if use_torch_compile and device == "cuda":
            try:
                if hasattr(self.pipeline, "unet"):
                    logger.info(f"Compiling unet with torch.compile(mode='{compile_mode}')...")
                    self.pipeline.unet = torch.compile(self.pipeline.unet, mode=compile_mode)

                if hasattr(self.pipeline, "transformer"):
                    logger.info(
                        f"Compiling transformer with torch.compile(mode='{compile_mode}')..."
                    )
                    self.pipeline.transformer = torch.compile(
                        self.pipeline.transformer, mode=compile_mode
                    )
                logger.info("torch.compile() applied for inference acceleration")
            except Exception as exc:
                logger.warning(f"torch.compile() failed (non-fatal): {exc}")

        logger.info(f"Model loaded in {time.time() - start:.1f}s")

        if use_torch_compile:
            logger.info(
                "Running warmup inference (first run triggers torch.compile — this may take several minutes)..."
            )
        else:
            logger.info("Running warmup inference...")
        warmup_start = time.time()
        try:
            with torch.inference_mode():
                _ = self.pipeline(
                    prompt="warmup",
                    width=512,
                    height=512,
                    num_inference_steps=2,
                    guidance_scale=1.0,
                )
            torch.cuda.synchronize()
            logger.info(f"Warmup complete in {time.time() - warmup_start:.1f}s")
        except Exception as exc:
            logger.warning(f"Warmup failed with torch.compile(), falling back to eager mode: {exc}")
            if hasattr(self.pipeline, "unet") and hasattr(self.pipeline.unet, "_orig_mod"):
                self.pipeline.unet = self.pipeline.unet._orig_mod
            if hasattr(self.pipeline, "transformer") and hasattr(self.pipeline.transformer, "_orig_mod"):
                self.pipeline.transformer = self.pipeline.transformer._orig_mod
            torch.cuda.empty_cache()
            warmup_start = time.time()
            with torch.inference_mode():
                _ = self.pipeline(
                    prompt="warmup",
                    width=512,
                    height=512,
                    num_inference_steps=2,
                    guidance_scale=1.0,
                )
            torch.cuda.synchronize()
            logger.info(f"Warmup complete (eager mode) in {time.time() - warmup_start:.1f}s")
