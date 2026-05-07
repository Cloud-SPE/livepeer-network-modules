import logging
import time
from typing import Optional

import numpy as np

logger = logging.getLogger("kokoro-runner")

KOKORO_SAMPLE_RATE = 24000


class KokoroPipeline:
    def __init__(self) -> None:
        self.pipeline: Optional[object] = None

    def load(self, lang_code: str, device: str, default_voice: str) -> None:
        logger.info(f"Loading Kokoro pipeline: lang_code={lang_code}, device={device}")

        start = time.time()
        from kokoro import KPipeline

        try:
            self.pipeline = KPipeline(lang_code=lang_code, device=device)
        except TypeError:
            self.pipeline = KPipeline(lang_code=lang_code)

        logger.info(f"Pipeline loaded in {time.time() - start:.1f}s")

        logger.info("Running warmup synthesis...")
        try:
            _ = self.synthesize("Hello.", default_voice, 1.0)
            logger.info("Warmup complete")
        except Exception as exc:
            logger.warning(f"Warmup failed (non-fatal): {exc}")

    def synthesize(self, text: str, voice: str, speed: float) -> np.ndarray:
        if self.pipeline is None:
            raise RuntimeError("Kokoro pipeline not loaded")

        chunks = []
        for result in self.pipeline(text, voice=voice, speed=speed):
            audio = getattr(result, "audio", None)
            if audio is None and isinstance(result, (tuple, list)) and len(result) >= 3:
                audio = result[2]
            if audio is None:
                continue
            if hasattr(audio, "detach"):
                audio = audio.detach().cpu().numpy()
            chunks.append(np.asarray(audio, dtype=np.float32))
        if not chunks:
            raise RuntimeError("Kokoro returned no audio")
        return np.concatenate(chunks)
