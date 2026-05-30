from __future__ import annotations

import tomllib
from pathlib import Path


EMBEDDER_DIR = Path(__file__).resolve().parents[1]


def test_sentence_transformers_has_major_version_upper_bound() -> None:
    pyproject = tomllib.loads((EMBEDDER_DIR / "pyproject.toml").read_text())

    dependencies = pyproject["project"]["dependencies"]

    assert "sentence-transformers>=3.3.1,<6" in dependencies


def test_dockerfile_uses_same_sentence_transformers_bound() -> None:
    dockerfile = (EMBEDDER_DIR / "Dockerfile").read_text()

    assert '"sentence-transformers>=3.3.1,<6"' in dockerfile
    assert '"sentence-transformers>=3.3.1"' not in dockerfile
