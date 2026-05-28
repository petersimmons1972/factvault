"""
Unit tests for the embedder FastAPI application.

These tests use FastAPI's TestClient which does NOT start the full lifespan
(i.e., the model is not loaded).  We monkey-patch _model so tests remain
fast and deterministic, with no network or GPU requirements.
"""

from __future__ import annotations

from unittest.mock import MagicMock

import numpy as np
import pytest
from fastapi.testclient import TestClient


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture()
def fake_model(monkeypatch):
    """Inject a fake SentenceTransformer that returns 1024-dim unit vectors."""
    import app as app_module

    mock = MagicMock()

    def _fake_encode(texts, convert_to_numpy=True, normalize_embeddings=True):
        result = np.zeros((len(texts), 1024), dtype=np.float32)
        result[:, 0] = 1.0  # non-zero first component
        return result

    mock.encode.side_effect = _fake_encode
    monkeypatch.setattr(app_module, "_model", mock)
    return mock


@pytest.fixture()
def client(fake_model):
    import app as app_module
    return TestClient(app_module.app)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_health(client) -> None:
    resp = client.get("/health")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"


def test_healthz(client) -> None:
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"


def test_info(client) -> None:
    resp = client.get("/info")
    assert resp.status_code == 200
    body = resp.json()
    assert body["dim"] == 1024
    assert "model" in body


def test_embed_returns_1024_dims(client) -> None:
    resp = client.post("/embed", json={"texts": ["hello world"]})
    assert resp.status_code == 200
    body = resp.json()
    assert "vectors" in body
    assert len(body["vectors"]) == 1
    assert len(body["vectors"][0]) == 1024


def test_embed_non_zero(client) -> None:
    """Embeddings must not be all-zero (stub detection)."""
    resp = client.post("/embed", json={"texts": ["factvault test"]})
    assert resp.status_code == 200
    vec = resp.json()["vectors"][0]
    assert any(v != 0.0 for v in vec), "vector is all-zero — stub still active?"


def test_embed_multiple_texts(client) -> None:
    texts = ["first sentence", "second sentence", "third"]
    resp = client.post("/embed", json={"texts": texts})
    assert resp.status_code == 200
    vectors = resp.json()["vectors"]
    assert len(vectors) == len(texts)
    for vec in vectors:
        assert len(vec) == 1024


def test_embed_empty_list(client) -> None:
    resp = client.post("/embed", json={"texts": []})
    assert resp.status_code == 200
    assert resp.json()["vectors"] == []
