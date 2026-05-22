"""
SHA-256 content hash helper.
"""
from __future__ import annotations

import hashlib


def compute_hash(body: bytes) -> str:
    """Compute a SHA-256 content hash with an algorithm prefix."""
    digest = hashlib.sha256(body).hexdigest()
    return f"sha256:{digest}"
