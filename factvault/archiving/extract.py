"""
trafilatura wrapper for raw_text extraction.
"""
from __future__ import annotations

import trafilatura


def extract_text(raw_html: bytes, url: str) -> str | None:
    """
    Extract plain text from raw HTML bytes using trafilatura.

    Returns None if extraction produces no useful text.
    """
    if not raw_html:
        return None

    html_str = raw_html.decode("utf-8", errors="replace")
    result = trafilatura.extract(
        html_str,
        url=url,
        output_format="txt",
        include_comments=False,
        include_tables=True,
        no_fallback=False,
        favor_precision=True,
    )
    if not result or not result.strip():
        return None
    return result.strip()
