import asyncio
import logging
import os
import uuid
from contextlib import asynccontextmanager
from typing import Optional

import torch
from fastapi import FastAPI, HTTPException
from fastapi.responses import Response
from pydantic import BaseModel

from .gpu_probe import fail_fast_if_cuda_requested_without_gpu
from .model_loader import DTYPE_MAP, CrossEncoderModel

MODEL_ID = os.environ.get("MODEL_ID", "zeroentropy/zerank-2")
MODEL_DIR = os.environ.get("MODEL_DIR", "/models")
RUNNER_PORT = int(os.environ.get("RUNNER_PORT", "8080"))
MAX_QUEUE_SIZE = int(os.environ.get("MAX_QUEUE_SIZE", "5"))
DEVICE = os.environ.get("DEVICE", "cuda")
DTYPE_STR = os.environ.get("DTYPE", "bfloat16")
MAX_BATCH_SIZE = int(os.environ.get("MAX_BATCH_SIZE", "1000"))
INFERENCE_BATCH_SIZE = int(os.environ.get("INFERENCE_BATCH_SIZE", "64"))
METRICS_ENABLED = os.environ.get("METRICS_ENABLED", "false").lower() in ("true", "1", "yes")

CAPABILITY_NAME = os.environ.get("CAPABILITY_NAME", "rerank")

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s")
logger = logging.getLogger("rerank-runner")

_cross_encoder = CrossEncoderModel()
_semaphore: Optional[asyncio.Semaphore] = None


class RerankRequest(BaseModel):
    query: str
    documents: list
    top_n: Optional[int] = None
    return_documents: bool = False
    model: Optional[str] = None


class RerankResultDocument(BaseModel):
    text: str


class RerankResult(BaseModel):
    index: int
    relevance_score: float
    document: Optional[RerankResultDocument] = None


class RerankResponse(BaseModel):
    id: str
    results: list[RerankResult]
    meta: dict


@asynccontextmanager
async def lifespan(app: FastAPI):
    global _semaphore

    fail_fast_if_cuda_requested_without_gpu(DEVICE)

    dtype = DTYPE_MAP.get(DTYPE_STR.lower(), torch.bfloat16)
    _cross_encoder.load(MODEL_ID, MODEL_DIR, DEVICE, dtype)

    _semaphore = asyncio.Semaphore(MAX_QUEUE_SIZE)

    logger.info(f"Rerank runner ready — model={MODEL_ID}, queue_size={MAX_QUEUE_SIZE}")
    yield

    logger.info("Shutting down, releasing GPU memory...")
    _cross_encoder.model = None
    if DEVICE == "cuda":
        torch.cuda.empty_cache()


app = FastAPI(title="rerank-runner", lifespan=lifespan)


def _extract_document_texts(documents: list) -> list[str]:
    texts = []
    for doc in documents:
        if isinstance(doc, str):
            texts.append(doc)
        elif isinstance(doc, dict) and "text" in doc:
            texts.append(doc["text"])
        else:
            raise ValueError(
                f"Invalid document format: expected string or dict with 'text' key, got {type(doc)}"
            )
    return texts


def _rerank_sync(query: str, doc_texts: list[str]) -> list[float]:
    pairs = [(query, doc) for doc in doc_texts]
    return _cross_encoder.predict(pairs, batch_size=INFERENCE_BATCH_SIZE)


@app.post("/v1/rerank", response_model=RerankResponse)
async def rerank(req: RerankRequest):
    if not req.query or not req.query.strip():
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": "query must be a non-empty string", "type": "invalid_request_error"}},
        )

    if not req.documents or len(req.documents) == 0:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": "documents must be a non-empty list", "type": "invalid_request_error"}},
        )

    if len(req.documents) > MAX_BATCH_SIZE:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": f"Too many documents: {len(req.documents)} (max {MAX_BATCH_SIZE})", "type": "invalid_request_error"}},
        )

    try:
        doc_texts = _extract_document_texts(req.documents)
    except ValueError as exc:
        raise HTTPException(
            status_code=400,
            detail={"error": {"message": str(exc), "type": "invalid_request_error"}},
        )

    if _semaphore.locked() and _semaphore._value == 0:
        raise HTTPException(
            status_code=429,
            detail={"error": {"message": f"Server busy — max queue size ({MAX_QUEUE_SIZE}) reached. Try again later.", "type": "rate_limit_error"}},
        )

    async with _semaphore:
        loop = asyncio.get_event_loop()
        try:
            scores = await loop.run_in_executor(None, _rerank_sync, req.query, doc_texts)
        except torch.cuda.OutOfMemoryError:
            torch.cuda.empty_cache()
            raise HTTPException(
                status_code=507,
                detail={"error": {"message": "GPU out of memory. Try fewer documents.", "type": "server_error"}},
            )
        except Exception as exc:
            logger.exception("Reranking failed")
            raise HTTPException(
                status_code=500,
                detail={"error": {"message": f"Inference failed: {exc}", "type": "server_error"}},
            )

    indexed_scores = list(enumerate(scores))
    indexed_scores.sort(key=lambda x: x[1], reverse=True)

    top_n = req.top_n if req.top_n is not None else len(indexed_scores)
    top_n = min(top_n, len(indexed_scores))
    indexed_scores = indexed_scores[:top_n]

    results = []
    for idx, score in indexed_scores:
        result = RerankResult(index=idx, relevance_score=score)
        if req.return_documents:
            result.document = RerankResultDocument(text=doc_texts[idx])
        results.append(result)

    return RerankResponse(
        id=f"rerank-{uuid.uuid4().hex[:12]}",
        results=results,
        meta={"model": MODEL_ID},
    )


@app.get("/healthz")
async def healthz():
    return {"status": "ok", "model": MODEL_ID, "device": DEVICE}


@app.get(f"/{CAPABILITY_NAME}/options")
async def options():
    return {"models": [MODEL_ID]}


if METRICS_ENABLED:
    from prometheus_client import CONTENT_TYPE_LATEST, generate_latest

    @app.get("/metrics")
    async def metrics():
        return Response(content=generate_latest(), media_type=CONTENT_TYPE_LATEST)
