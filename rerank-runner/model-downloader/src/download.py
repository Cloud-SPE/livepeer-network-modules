#!/usr/bin/env python3
import logging
import os
import sys
import time

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("model-downloader")


def main() -> int:
    model_id = os.environ.get("MODEL_ID", "zeroentropy/zerank-2")
    model_dir = os.environ.get("MODEL_DIR", "/models")
    hf_token = os.environ.get("HF_TOKEN", None)

    logger.info(f"Model to download: {model_id}")
    logger.info(f"Download directory: {model_dir}")

    os.makedirs(model_dir, exist_ok=True)

    from huggingface_hub import snapshot_download

    start = time.time()

    try:
        path = snapshot_download(
            model_id,
            cache_dir=model_dir,
            token=hf_token,
            resume_download=True,
        )
        elapsed = time.time() - start
        logger.info(f"Model downloaded to {path} ({elapsed:.0f}s)")
    except Exception as exc:
        elapsed = time.time() - start
        logger.error(f"Download failed: {exc} ({elapsed:.0f}s)")
        return 1

    logger.info("Download complete")
    return 0


if __name__ == "__main__":
    sys.exit(main())
