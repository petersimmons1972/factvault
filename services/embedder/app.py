"""
Factvault embedder service — BGE-M3 via sentence-transformers (CPU).

Model:  BAAI/bge-m3  (1024-dimensional dense embeddings)
Cache:  /home/nonroot/.cache  (compose volume: embedder-cache)
        Set via HF_HOME env var so HuggingFace Hub respects it too.
"""

from __future__ import annotations

import os
import time
import logging
from contextlib import asynccontextmanager
from typing import AsyncIterator

from fastapi import FastAPI, HTTPException
from fastapi.responses import JSONResponse
from pydantic import BaseModel

logging.basicConfig(level=logging.INFO, format="%(levelname)s  %(message)s")
log = logging.getLogger(__name__)

MODEL_NAME = os.environ.get("EMBEDDER_MODEL", "BAAI/bge-m3")
CACHE_DIR = os.environ.get("HF_HOME", "/home/nonroot/.cache")
EXPECTED_DIM = 1024
DEFAULT_MAX_EMBED_TEXTS = 512
DEFAULT_MAX_EMBED_BYTES = 2 * 1024 * 1024

# Module-level reference; populated during lifespan startup.
_model = None  # type: ignore[assignment]


class EmbedRequest(BaseModel):
    texts: list[str]


class EmbedResponse(BaseModel):
    vectors: list[list[float]]


class InfoResponse(BaseModel):
    model: str
    dim: int


def _int_env(name: str, default: int) -> int:
    raw = os.environ.get(name)
    if raw is None:
        return default
    try:
        value = int(raw)
    except ValueError:
        log.warning("Invalid %s=%r; falling back to %d", name, raw, default)
        return default
    if value <= 0:
        log.warning("Non-positive %s=%r; falling back to %d", name, raw, default)
        return default
    return value


def max_embed_texts() -> int:
    return _int_env("MAX_EMBED_TEXTS", DEFAULT_MAX_EMBED_TEXTS)


def max_embed_bytes() -> int:
    return _int_env("MAX_EMBED_BYTES", DEFAULT_MAX_EMBED_BYTES)


@asynccontextmanager
async def lifespan(app: FastAPI) -> AsyncIterator[None]:
    global _model
    log.info("Loading model %s (cache=%s) …", MODEL_NAME, CACHE_DIR)
    t0 = time.perf_counter()

    # Delay-import sentence-transformers so the import error surfaces clearly.
    from sentence_transformers import SentenceTransformer  # noqa: PLC0415

    _model = SentenceTransformer(
        MODEL_NAME,
        cache_folder=CACHE_DIR,
        device="cpu",
    )
    # Warm-up: one encode to trigger lazy initialisation.
    _model.encode(["warmup"], convert_to_numpy=True)
    elapsed = time.perf_counter() - t0
    log.info("Model ready in %.1fs (dim=%d)", elapsed, EXPECTED_DIM)
    yield
    _model = None


app = FastAPI(lifespan=lifespan)


# C6: /health endpoint removed; doctor checks probe only /healthz.
@app.get("/healthz")
def health() -> JSONResponse:
    """Model-aware health.

    Returns 200 only when the model is loaded and ready to serve /embed.
    Returns 503 with {"status":"loading"} while the lifespan startup is
    still pulling/initialising weights — this prevents dependent services
    (compose `depends_on: condition: service_healthy`) from racing ahead
    and hitting `/embed` before the model is ready.
    """
    if _model is None:
        return JSONResponse(status_code=503, content={"status": "loading"})
    return JSONResponse(status_code=200, content={"status": "ok"})


@app.get("/info", response_model=InfoResponse)
def info() -> InfoResponse:
    return InfoResponse(model=MODEL_NAME, dim=EXPECTED_DIM)


@app.post("/embed", response_model=EmbedResponse)
def embed(req: EmbedRequest) -> EmbedResponse:
    if len(req.texts) > max_embed_texts():
        raise HTTPException(status_code=422, detail=f"too many texts: max {max_embed_texts()}")
    total_bytes = sum(len(text.encode()) for text in req.texts)
    if total_bytes > max_embed_bytes():
        raise HTTPException(status_code=422, detail=f"total text bytes exceed max {max_embed_bytes()}")
    if not req.texts:
        return EmbedResponse(vectors=[])
    if _model is None:
        # 503 (not 500) — transient unavailability while the model loads.
        # Clients with retry/backoff will recover; explicit 500 would mislead.
        raise HTTPException(status_code=503, detail="model not yet loaded")
    embeddings = _model.encode(req.texts, convert_to_numpy=True, normalize_embeddings=True)
    return EmbedResponse(vectors=embeddings.tolist())
