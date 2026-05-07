import logging
import time
from typing import Optional

import torch

logger = logging.getLogger("rerank-runner")

DTYPE_MAP = {
    "float16": torch.float16,
    "fp16": torch.float16,
    "bfloat16": torch.bfloat16,
    "bf16": torch.bfloat16,
    "float32": torch.float32,
    "fp32": torch.float32,
}


class CrossEncoderModel:
    def __init__(self) -> None:
        self.model: Optional[object] = None

    def load(self, model_id: str, model_dir: str, device: str, dtype: torch.dtype) -> None:
        logger.info(f"Loading model: {model_id} (dtype={dtype}, device={device})")
        logger.info(f"Model cache directory: {model_dir}")

        start = time.time()
        from sentence_transformers import CrossEncoder

        self.model = CrossEncoder(
            model_id,
            trust_remote_code=True,
            device=device,
            cache_folder=model_dir,
        )

        if self.model.tokenizer.pad_token is None:
            self.model.tokenizer.pad_token = self.model.tokenizer.eos_token
            self.model.tokenizer.pad_token_id = self.model.tokenizer.eos_token_id
        if self.model.model.config.pad_token_id is None:
            self.model.model.config.pad_token_id = self.model.tokenizer.pad_token_id
        logger.info(
            f"pad_token='{self.model.tokenizer.pad_token}' "
            f"(id={self.model.tokenizer.pad_token_id})"
        )

        if dtype != torch.float32:
            self.model.model = self.model.model.to(dtype=dtype)

        logger.info(f"Model loaded in {time.time() - start:.1f}s")

        logger.info("Running warmup inference...")
        warmup_start = time.time()
        try:
            _ = self.model.predict([("warmup query", "warmup document")], batch_size=1)
            if device == "cuda":
                torch.cuda.synchronize()
            logger.info(f"Warmup complete in {time.time() - warmup_start:.1f}s")
        except Exception as exc:
            logger.warning(f"Warmup failed (non-fatal): {exc}")

    def predict(self, pairs: list[tuple[str, str]], batch_size: int) -> list[float]:
        if self.model is None:
            raise RuntimeError("CrossEncoder model not loaded")
        scores = self.model.predict(pairs, batch_size=batch_size)
        return [float(s) for s in scores]
