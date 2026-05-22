"""
Tests for the SHA-256 content hash helper.
"""
from __future__ import annotations

import hashlib

from factvault.archiving.hash import compute_hash


def test_compute_hash_returns_sha256_prefix() -> None:
    result = compute_hash(b"hello world")
    assert result.startswith("sha256:")


def test_compute_hash_known_value() -> None:
    expected = "sha256:" + hashlib.sha256(b"hello world").hexdigest()
    result = compute_hash(b"hello world")
    assert result == expected


def test_compute_hash_empty_bytes() -> None:
    expected = "sha256:" + hashlib.sha256(b"").hexdigest()
    result = compute_hash(b"")
    assert result == expected


def test_compute_hash_different_inputs_differ() -> None:
    assert compute_hash(b"content one") != compute_hash(b"content two")


def test_compute_hash_same_input_deterministic() -> None:
    body = b"some article body " * 100
    assert compute_hash(body) == compute_hash(body)


def test_compute_hash_returns_string() -> None:
    result = compute_hash(b"test")
    assert isinstance(result, str)


def test_compute_hash_hex_length() -> None:
    result = compute_hash(b"test data")
    assert len(result) == len("sha256:") + 64


def test_compute_hash_algorithm_prefix_detectable() -> None:
    result = compute_hash(b"data")
    algo, hexdigest = result.split(":", 1)
    assert algo == "sha256"
    assert len(hexdigest) == 64
