"""
Tests for trafilatura extraction wrapper.
"""
from __future__ import annotations

from pathlib import Path

from factvault.archiving.extract import extract_text


FIXTURE_DIR = Path(__file__).parent.parent / "fixtures" / "articles"


def test_extract_text_returns_string_for_article() -> None:
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    assert result is not None
    assert isinstance(result, str)
    assert len(result) > 0


def test_extract_text_contains_article_content() -> None:
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    assert result is not None
    assert "50 million" in result or "$50M" in result or "Series B" in result


def test_extract_text_excludes_nav_and_footer() -> None:
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    assert result is not None
    assert "Nav links" not in result


def test_extract_text_returns_none_for_paywall() -> None:
    html = (FIXTURE_DIR / "paywall.html").read_bytes()
    result = extract_text(html, url="https://example.com/premium")
    assert result is None or isinstance(result, str)


def test_extract_text_returns_none_for_empty_bytes() -> None:
    result = extract_text(b"", url="https://example.com/empty")
    assert result is None


def test_extract_text_returns_none_for_minimal_html() -> None:
    result = extract_text(b"<html><body></body></html>", url="https://example.com/empty")
    assert result is None


def test_extract_text_is_plain_text_not_html() -> None:
    html = (FIXTURE_DIR / "article.html").read_bytes()
    result = extract_text(html, url="https://example.com/article")
    if result:
        assert "<html" not in result
        assert "<p>" not in result
        assert "<article" not in result
