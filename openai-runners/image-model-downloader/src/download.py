#!/usr/bin/env python3
import logging
import os
import sys
import time

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("image-model-downloader")


def main() -> int:
    model_ids_str = os.environ.get("MODEL_IDS", "SG161222/RealVisXL_V4.0_Lightning")
    model_dir = os.environ.get("MODEL_DIR", "/models")
    hf_token = os.environ.get("HF_TOKEN", None)

    if not model_ids_str:
        logger.error("MODEL_IDS environment variable is required")
        logger.error('Example: MODEL_IDS="SG161222/RealVisXL_V4.0_Lightning,black-forest-labs/FLUX.1-dev"')
        return 1

    model_ids = [m.strip() for m in model_ids_str.split(",") if m.strip()]
    logger.info(f"Models to download: {model_ids}")
    logger.info(f"Download directory: {model_dir}")

    os.makedirs(model_dir, exist_ok=True)

    from huggingface_hub import snapshot_download, try_to_load_from_cache

    results = []
    for model_id in model_ids:
        logger.info("=" * 60)
        logger.info(f"Checking: {model_id}")
        start = time.time()

        try:
            cached = try_to_load_from_cache(model_id, "model_index.json", cache_dir=model_dir)
            if cached is not None:
                logger.info(f"{model_id} already downloaded, verifying...")

            path = snapshot_download(
                model_id,
                cache_dir=model_dir,
                token=hf_token,
                resume_download=True,
            )
            elapsed = time.time() - start
            if cached is not None:
                logger.info(f"{model_id} verified at {path} ({elapsed:.0f}s)")
            else:
                logger.info(f"{model_id} downloaded to {path} ({elapsed:.0f}s)")
            results.append((model_id, "ok", elapsed))
        except Exception as exc:
            elapsed = time.time() - start
            logger.error(f"{model_id} failed: {exc}")
            results.append((model_id, f"error: {exc}", elapsed))

    logger.info("=" * 60)
    logger.info("Download Summary:")
    all_ok = True
    for model_id, status, elapsed in results:
        logger.info(f"  {model_id} — {status} ({elapsed:.0f}s)")
        if status != "ok":
            all_ok = False

    if not all_ok:
        logger.error("Some downloads failed")
        return 1

    logger.info("All models downloaded successfully")
    return 0


if __name__ == "__main__":
    sys.exit(main())
