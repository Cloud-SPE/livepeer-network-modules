import logging
import time
from typing import Optional

import numpy as np
import torch

logger = logging.getLogger("whisper-runner")

DTYPE_MAP = {
    "float16": torch.float16,
    "fp16": torch.float16,
    "bfloat16": torch.bfloat16,
    "bf16": torch.bfloat16,
    "float32": torch.float32,
    "fp32": torch.float32,
}

TARGET_SAMPLE_RATE = 16000


class WhisperPipeline:
    def __init__(self) -> None:
        self.pipeline: Optional[object] = None

    def load(
        self,
        model_id: str,
        model_dir: str,
        device: str,
        dtype: torch.dtype,
        chunk_length_s: int,
        inference_batch_size: int,
    ) -> None:
        logger.info(f"Loading model: {model_id} (dtype={dtype}, device={device})")
        logger.info(f"Model cache directory: {model_dir}")

        start = time.time()

        from transformers import (
            AutoModelForSpeechSeq2Seq,
            AutoProcessor,
            pipeline,
        )

        model = AutoModelForSpeechSeq2Seq.from_pretrained(
            model_id,
            torch_dtype=dtype,
            cache_dir=model_dir,
            low_cpu_mem_usage=True,
            use_safetensors=True,
        ).to(device)

        processor = AutoProcessor.from_pretrained(model_id, cache_dir=model_dir)

        self.pipeline = pipeline(
            "automatic-speech-recognition",
            model=model,
            tokenizer=processor.tokenizer,
            feature_extractor=processor.feature_extractor,
            chunk_length_s=chunk_length_s,
            batch_size=inference_batch_size,
            torch_dtype=dtype,
            device=device,
            return_timestamps=True,
        )

        logger.info(f"Model loaded in {time.time() - start:.1f}s")

        logger.info("Running warmup inference...")
        warmup_start = time.time()
        try:
            warmup_audio = np.zeros(TARGET_SAMPLE_RATE, dtype=np.float32)
            _ = self.pipeline(warmup_audio.copy(), generate_kwargs={"task": "transcribe"})
            if device == "cuda":
                torch.cuda.synchronize()
            logger.info(f"Warmup complete in {time.time() - warmup_start:.1f}s")
        except Exception as exc:
            logger.warning(f"Warmup failed (non-fatal): {exc}")

    def run(
        self,
        audio: np.ndarray,
        task: str,
        language: Optional[str],
        prompt: Optional[str],
        temperature: float,
    ) -> dict:
        generate_kwargs: dict = {"task": task}
        if language:
            generate_kwargs["language"] = language
        if temperature and temperature > 0:
            generate_kwargs["temperature"] = float(temperature)
        if prompt and self.pipeline is not None:
            prompt_ids = self.pipeline.tokenizer.get_prompt_ids(
                prompt, return_tensors="pt"
            ).to(self.pipeline.device)
            generate_kwargs["prompt_ids"] = prompt_ids
        return self.pipeline(audio, generate_kwargs=generate_kwargs, return_timestamps=True)
