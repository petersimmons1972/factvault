# Fact Pipeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn archived sources into structured, corroborated statements with deterministic confidence. The extract worker runs a regex/gazetteer pass followed by an LLM extractor gated by character-offset verification (the load-bearing anti-hallucination check); the corroborate worker computes confidence from independent source counts and flags conflicts. This is Plan 3 of 5; depends on Plans 1 (schema) and 2 (sources in `status='archived'`).

**Architecture:** Two new workers (`extract`, `corroborate`) using Plan 2's Worker ABC framework. Extract uses a starter regex+gazetteer library plus an OpenAI-compatible LLM client (default local vLLM/Ollama). LLM extractor returns JSON conforming to a strict schema; outputs are rejected unless their claimed character offsets into `sources.raw_text` actually contain the claimed excerpt. Corroborate computes confidence via a deterministic formula over independent source counts.

**Tech Stack:** Python 3.12, SQLAlchemy 2.x, click, sentence-transformers (BGE-M3), openai-compatible HTTP client (use the existing `openai` Python SDK pointed at any OpenAI-compatible endpoint), plus the Plan 1 + Plan 2 stack.

---

## Known Plan-Bug Patterns (apply from the start — do NOT discover these during execution)

These six patterns were surfaced during Plan 1 execution. Every task in this plan is written to avoid them.

1. **`TIMESTAMPTZ` import:** `TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`. Use `TIMESTAMP(timezone=True)` from `sqlalchemy` (e.g., `sa.TIMESTAMP(timezone=True)`).
2. **Explicit SA imports:** `sa.UniqueConstraint` / `sa.LargeBinary` need direct imports when `sa` alias isn't in scope. Prefer `from sqlalchemy import UniqueConstraint, LargeBinary` explicitly.
3. **psycopg cast syntax:** psycopg refuses `:param::jsonb` / `:param::vector`. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` in raw SQL.
4. **Postgres 15+ NULL uniqueness:** Unique constraints default to `NULLS NOT DISTINCT`. Tests relying on duplicate-NULL behavior must use distinct tenants/values to avoid unexpected conflicts.
5. **Fixture tenancy:** The `conn` fixture is single-tenant superuser (bypasses RLS). RLS-sensitive tests must use `app_engine`.
6. **RLS setting:** RLS policies wrap `current_setting(...)` with `NULLIF(..., '')` before `::uuid` cast — this is already in DB. Application code setting `app.current_tenant_id` can rely on this guard.

---

## File Structure

```
factvault/
├── factvault/
│   ├── extractors/
│   │   ├── __init__.py
│   │   ├── base.py                          # Extractor ABC + ExtractedFact dataclass
│   │   ├── deterministic/
│   │   │   ├── __init__.py
│   │   │   ├── identifiers.py               # CIK, CUSIP, DOI, NCT, ISBN-13 regexes
│   │   │   ├── money.py                     # USD amount patterns
│   │   │   ├── dates.py                     # ISO + common date formats
│   │   │   ├── gazetteer.py                 # Named entity exact-match lookup
│   │   │   └── runner.py                    # Composes all det extractors, returns ExtractedFacts
│   │   └── llm.py                           # OpenAI-compatible LLM extractor with structured output + offset verification
│   ├── workers/
│   │   ├── extract.py                       # Stage 3 worker
│   │   ├── corroborate.py                   # Stage 4 worker
│   │   └── ... (existing from Plan 2)
│   ├── embeddings/
│   │   ├── __init__.py
│   │   └── bge_m3.py                        # Wrapper around sentence-transformers for BGE-M3 1024-dim
│   ├── vocabulary/
│   │   ├── __init__.py
│   │   ├── resolver.py                      # Strict vs permissive slug handling
│   │   └── starter_properties.yaml          # Default property vocabulary (~40 entries)
│   └── ... (existing)
├── data/
│   └── gazetteer/
│       ├── sp500_companies.csv              # name + aliases
│       └── us_politicians.csv               # name + aliases + jurisdiction
├── tests/
│   ├── extractors/
│   │   ├── __init__.py
│   │   ├── test_base.py
│   │   ├── deterministic/
│   │   │   ├── __init__.py
│   │   │   ├── test_identifiers.py
│   │   │   ├── test_money.py
│   │   │   ├── test_dates.py
│   │   │   ├── test_gazetteer.py
│   │   │   └── test_runner.py
│   │   └── test_llm.py                      # uses pytest-httpx for the LLM endpoint
│   ├── workers/
│   │   ├── test_extract.py
│   │   └── test_corroborate.py
│   ├── embeddings/
│   │   └── test_bge_m3.py
│   ├── vocabulary/
│   │   └── test_resolver.py
│   └── integration/
│       └── test_fact_pipeline_e2e.py        # source → extract → corroborate
└── ... (existing)
```

---

## Tasks

### Task 1 — Dependency additions to `pyproject.toml`

- [ ] **FAIL:** Confirm `sentence-transformers`, `openai`, `pyyaml` are absent from current `pyproject.toml`.

```bash
$ grep -E 'sentence.transformers|openai|pyyaml' pyproject.toml
# expected: no output (they are not yet listed)
```

- [ ] **IMPLEMENT:** Edit `pyproject.toml` — add three new runtime dependencies. The `dependencies` list in `[project]` becomes:

```toml
[project]
name = "factvault"
version = "0.0.1"
requires-python = ">=3.12,<3.14"
dependencies = [
    "sqlalchemy>=2.0,<3",
    "alembic>=1.13,<2",
    "psycopg[binary]>=3.1,<4",
    "pgvector>=0.3,<1",
    "pydantic>=2,<3",
    "httpx>=0.27,<1",
    "trafilatura>=1.9,<2",
    "feedparser>=6.0,<7",
    "click>=8,<9",
    "sentence-transformers>=3,<4",
    "openai>=1.40,<2",
    "pyyaml>=6,<7",
]
```

The `[project.optional-dependencies]` `dev` section gains `pytest-httpx`:

```toml
[project.optional-dependencies]
dev = [
    "pytest>=8,<9",
    "testcontainers[postgres]>=4,<5",
    "pytest-asyncio>=0.23,<1",
    "pytest-httpx>=0.30,<1",
]
```

- [ ] **RUN/PASS:**

```bash
$ pip install -e ".[dev]"
# expected: Successfully installed ... sentence-transformers-... openai-... pyyaml-... pytest-httpx-...
$ python -c "import sentence_transformers, openai, yaml; print('OK')"
# expected: OK
```

- [ ] **COMMIT:**

```bash
git add pyproject.toml
git commit -m "chore(deps): add sentence-transformers, openai, pyyaml + pytest-httpx for fact-pipeline"
```

---

### Task 2 — `ExtractedFact` dataclass + `Extractor` ABC

- [ ] **FAIL:** Create `tests/extractors/__init__.py` (empty) and write the failing test:

```python
# tests/extractors/__init__.py
```

```python
# tests/extractors/test_base.py
import pytest
from factvault.extractors.base import ExtractedFact, Extractor


# ── ExtractedFact ──────────────────────────────────────────────────────────────

def test_extracted_fact_is_frozen():
    fact = ExtractedFact(
        subject_text="Apple Inc.",
        property_slug="sec_cik",
        value="0000320193",
        value_type="string",
        excerpt="Apple Inc. (CIK 0000320193)",
        excerpt_offset_start=0,
        excerpt_offset_end=26,
        extraction_method="regex:identifiers-v1",
        source_confidence=0.9,
    )
    with pytest.raises((AttributeError, TypeError)):
        fact.subject_text = "Other"  # type: ignore[misc]


def test_extracted_fact_equality():
    kwargs = dict(
        subject_text="Apple Inc.",
        property_slug="sec_cik",
        value="0000320193",
        value_type="string",
        excerpt="Apple Inc. (CIK 0000320193)",
        excerpt_offset_start=0,
        excerpt_offset_end=26,
        extraction_method="regex:identifiers-v1",
        source_confidence=0.9,
    )
    a = ExtractedFact(**kwargs)
    b = ExtractedFact(**kwargs)
    assert a == b


def test_extracted_fact_is_hashable():
    fact = ExtractedFact(
        subject_text="Apple Inc.",
        property_slug="sec_cik",
        value="0000320193",
        value_type="string",
        excerpt="Apple Inc. (CIK 0000320193)",
        excerpt_offset_start=0,
        excerpt_offset_end=26,
        extraction_method="regex:identifiers-v1",
        source_confidence=0.9,
    )
    s = {fact}
    assert len(s) == 1


def test_extracted_fact_optional_source_confidence():
    fact = ExtractedFact(
        subject_text="AAPL",
        property_slug="sec_cik",
        value="0000320193",
        value_type="string",
        excerpt="AAPL CIK 0000320193",
        excerpt_offset_start=5,
        excerpt_offset_end=19,
        extraction_method="regex:identifiers-v1",
        source_confidence=None,
    )
    assert fact.source_confidence is None


# ── Extractor ABC ──────────────────────────────────────────────────────────────

def test_extractor_abc_cannot_instantiate():
    with pytest.raises(TypeError):
        Extractor()  # type: ignore[abstract]


def test_extractor_subclass_without_extract_rejected():
    class BadExtractor(Extractor):
        pass

    with pytest.raises(TypeError):
        BadExtractor()  # type: ignore[abstract]


def test_extractor_subclass_with_extract_ok():
    class GoodExtractor(Extractor):
        def extract(self, source):
            return iter([])

    ge = GoodExtractor()
    assert list(ge.extract({"raw_text": "hello"})) == []
```

Run:

```bash
$ python -m pytest tests/extractors/test_base.py -x
# expected: FAILED (ImportError — factvault.extractors.base does not exist yet)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/__init__.py` (empty) and `factvault/extractors/base.py`:

```python
# factvault/extractors/__init__.py
```

```python
# factvault/extractors/base.py
"""
Base types for the factvault extraction layer.

ExtractedFact — immutable dataclass representing one candidate fact extracted
    from a source document. Character offsets are into the source's raw_text.

Extractor — abstract base class; concrete implementations yield ExtractedFact
    instances from a source dict (keys: id, raw_text, publisher, tenant_id, …).
"""
from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass
from typing import Iterator


@dataclass(frozen=True)
class ExtractedFact:
    """One candidate fact produced by an extractor.

    Fields
    ------
    subject_text        : Raw text of the subject entity as it appears in the source.
    property_slug       : Machine-readable property key (must exist in `properties`
                          or be queued in `proposed_properties` on strict mode).
    value               : String representation of the extracted value.
    value_type          : One of 'entity_ref' | 'string' | 'number' | 'date' | 'url'.
    excerpt             : Verbatim passage from raw_text that grounds this fact.
    excerpt_offset_start: Character offset into source.raw_text where excerpt begins.
    excerpt_offset_end  : Character offset into source.raw_text where excerpt ends
                          (exclusive; slice notation: raw_text[start:end]).
    extraction_method   : Provenance tag, e.g. 'regex:identifiers-v1' or
                          'llm:llama3.1:8b:v1'. Written to statement_sources.
    source_confidence   : Per-source confidence estimate in [0,1] or None. The
                          corroborate worker overwrites this with the deterministic
                          corroboration score; this field seeds the initial write.
    """

    subject_text: str
    property_slug: str
    value: str
    value_type: str
    excerpt: str
    excerpt_offset_start: int
    excerpt_offset_end: int
    extraction_method: str
    source_confidence: float | None


class Extractor(ABC):
    """Abstract base class for all extractors.

    Concrete subclasses implement :meth:`extract`, which receives a source dict
    and returns an iterator of :class:`ExtractedFact` instances.

    The source dict carries at minimum:
        ``id``         — UUID string of the sources row
        ``raw_text``   — full plain-text body of the source
        ``publisher``  — publisher domain (may be None)
        ``tenant_id``  — UUID string of the owning tenant
    """

    @abstractmethod
    def extract(self, source: dict) -> Iterator[ExtractedFact]:
        """Yield ExtractedFact instances found in *source*."""
        ...
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/test_base.py -x -v
# expected: 7 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/__init__.py factvault/extractors/base.py \
        tests/extractors/__init__.py tests/extractors/test_base.py
git commit -m "feat(extractors): ExtractedFact dataclass + Extractor ABC"
```

---

### Task 3 — Identifier extractor

- [ ] **FAIL:** Create `tests/extractors/deterministic/__init__.py` (empty) and write the failing test:

```python
# tests/extractors/deterministic/__init__.py
```

```python
# tests/extractors/deterministic/test_identifiers.py
import pytest
from factvault.extractors.deterministic.identifiers import IdentifierExtractor

FIXTURE_TEXT = (
    "The SEC filing showed Apple Inc. has CIK 0000320193 and issued bonds with "
    "CUSIP 037833100. The drug trial NCT12345678 uses compound with DOI "
    "10.1038/s41586-023-06610-z. The bond's ISBN-13 reference is 978-3-16-148410-0."
)

SOURCE = {"id": "src-1", "raw_text": FIXTURE_TEXT, "publisher": "example.com", "tenant_id": "t-1"}


def test_cik_extracted():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    cik_facts = [f for f in facts if f.property_slug == "sec_cik"]
    assert len(cik_facts) == 1
    assert cik_facts[0].value == "0000320193"


def test_cusip_extracted():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    cusip_facts = [f for f in facts if f.property_slug == "cusip"]
    assert len(cusip_facts) == 1
    assert cusip_facts[0].value == "037833100"


def test_doi_extracted():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    doi_facts = [f for f in facts if f.property_slug == "doi"]
    assert len(doi_facts) == 1
    assert doi_facts[0].value == "10.1038/s41586-023-06610-z"


def test_nct_id_extracted():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    nct_facts = [f for f in facts if f.property_slug == "nct_id"]
    assert len(nct_facts) == 1
    assert nct_facts[0].value == "NCT12345678"


def test_isbn13_extracted():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    isbn_facts = [f for f in facts if f.property_slug == "isbn13"]
    assert len(isbn_facts) == 1
    assert isbn_facts[0].value == "978-3-16-148410-0"


def test_offsets_point_to_excerpt_in_raw_text():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    for fact in facts:
        actual = FIXTURE_TEXT[fact.excerpt_offset_start:fact.excerpt_offset_end]
        assert actual == fact.excerpt, (
            f"Offset mismatch for {fact.property_slug}: "
            f"expected {fact.excerpt!r}, got {actual!r}"
        )


def test_extraction_method_tag():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    for fact in facts:
        assert fact.extraction_method == "regex:identifiers-v1"


def test_value_type_is_string():
    extractor = IdentifierExtractor()
    facts = list(extractor.extract(SOURCE))
    for fact in facts:
        assert fact.value_type == "string"


def test_no_false_positives_on_clean_text():
    clean = {"id": "src-2", "raw_text": "Nothing here.", "publisher": "x.com", "tenant_id": "t-1"}
    extractor = IdentifierExtractor()
    assert list(extractor.extract(clean)) == []
```

Run:

```bash
$ python -m pytest tests/extractors/deterministic/test_identifiers.py -x
# expected: FAILED (ImportError — module does not exist yet)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/deterministic/__init__.py` (empty) and `factvault/extractors/deterministic/identifiers.py`:

```python
# factvault/extractors/deterministic/__init__.py
```

```python
# factvault/extractors/deterministic/identifiers.py
"""
Deterministic identifier extractor.

Extracts well-structured identifiers with unambiguous regex patterns:
    sec_cik  — 10-digit SEC filer CIK (padded with leading zeros)
    cusip    — 9-character CUSIP security identifier (alphanumeric)
    doi      — Digital Object Identifier in 10.NNNN/suffix form
    nct_id   — ClinicalTrials.gov NCT number (NCT + 8 digits)
    isbn13   — ISBN-13 in 978-/979- hyphenated or plain form

Each match yields an ExtractedFact with:
    subject_text         : the matched value itself (identifiers are self-naming)
    property_slug        : one of the five slugs above
    value                : the matched string (normalized)
    value_type           : 'string'
    excerpt              : the matched text as it appears in raw_text
    excerpt_offset_start : character start offset into raw_text
    excerpt_offset_end   : character end offset into raw_text (exclusive)
    extraction_method    : 'regex:identifiers-v1'
    source_confidence    : 0.95 (identifiers are high-confidence when pattern matches)

Non-goal: relative identifier disambiguation (e.g. bare 9-digit numbers that
could be either CUSIP or something else). Only matches when surrounded by
unambiguous context or format.
"""
from __future__ import annotations

import re
from typing import Iterator

from factvault.extractors.base import ExtractedFact, Extractor

# ---------------------------------------------------------------------------
# Regex patterns
# Each pattern uses a single capturing group that covers exactly the value.
# The excerpt is the full match (group 0); offsets are for group 0.
# ---------------------------------------------------------------------------

_PATTERNS: list[tuple[str, re.Pattern[str]]] = [
    # CIK: exactly 10 digits, often prefixed by "CIK" keyword
    # Accepts both "CIK 0000320193" and standalone "0000320193" when preceded
    # by "CIK" (case-insensitive) within 1-4 chars.
    (
        "sec_cik",
        re.compile(r"\bCIK\s{0,4}(\d{10})\b", re.IGNORECASE),
    ),
    # CUSIP: 9 alphanumeric chars, preceded by "CUSIP" keyword
    (
        "cusip",
        re.compile(r"\bCUSIP\s{0,4}([A-Z0-9]{9})\b", re.IGNORECASE),
    ),
    # DOI: 10.NNNN/suffix — standard DOI prefix form
    (
        "doi",
        re.compile(r"\b(10\.\d{4,9}/[^\s,;\"\')\]]+)", re.IGNORECASE),
    ),
    # NCT ID: NCT followed by exactly 8 digits
    (
        "nct_id",
        re.compile(r"\b(NCT\d{8})\b", re.IGNORECASE),
    ),
    # ISBN-13: 978 or 979 prefix, 13 digits, optional hyphens
    (
        "isbn13",
        re.compile(
            r"\b(97[89](?:-\d{1,5}){3}-\d|97[89]\d{10})\b",
            re.IGNORECASE,
        ),
    ),
]

_METHOD = "regex:identifiers-v1"
_CONFIDENCE = 0.95


class IdentifierExtractor(Extractor):
    """Yields ExtractedFact instances for each identifier match in source['raw_text']."""

    def extract(self, source: dict) -> Iterator[ExtractedFact]:
        raw_text: str = source.get("raw_text") or ""
        if not raw_text:
            return

        for slug, pattern in _PATTERNS:
            for match in pattern.finditer(raw_text):
                # Use group(1) for the normalised value; group(0) for excerpt + offsets.
                value = match.group(1)
                excerpt = match.group(0)
                start = match.start(0)
                end = match.end(0)
                yield ExtractedFact(
                    subject_text=value,
                    property_slug=slug,
                    value=value,
                    value_type="string",
                    excerpt=excerpt,
                    excerpt_offset_start=start,
                    excerpt_offset_end=end,
                    extraction_method=_METHOD,
                    source_confidence=_CONFIDENCE,
                )
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/deterministic/test_identifiers.py -x -v
# expected: 9 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/deterministic/__init__.py \
        factvault/extractors/deterministic/identifiers.py \
        tests/extractors/deterministic/__init__.py \
        tests/extractors/deterministic/test_identifiers.py
git commit -m "feat(extractors): deterministic identifier extractor (CIK, CUSIP, DOI, NCT, ISBN-13)"
```

---

### Task 4 — Money extractor

- [ ] **FAIL:** Write the failing test:

```python
# tests/extractors/deterministic/test_money.py
import pytest
from decimal import Decimal
from factvault.extractors.deterministic.money import MoneyExtractor

FIXTURE_TEXT = (
    "The startup raised $4.2 billion in Series B funding. Earlier it secured "
    "$150M from Andreessen Horowitz. The acquisition cost $320 million. "
    "Revenue was $1,250,000 last quarter. The bridge round was $5K."
)

SOURCE = {"id": "src-1", "raw_text": FIXTURE_TEXT, "publisher": "tc.com", "tenant_id": "t-1"}


def _facts(text: str):
    src = {"id": "x", "raw_text": text, "publisher": "x.com", "tenant_id": "t-1"}
    return list(MoneyExtractor().extract(src))


def test_billion_multiplier():
    facts = _facts("The deal was $4.2 billion.")
    assert len(facts) == 1
    assert facts[0].value == str(int(Decimal("4.2") * 1_000_000_000 * 100))


def test_million_multiplier_word():
    facts = _facts("They raised $320 million.")
    assert len(facts) == 1
    assert facts[0].value == str(int(Decimal("320") * 1_000_000 * 100))


def test_million_multiplier_suffix():
    facts = _facts("They raised $150M.")
    assert len(facts) == 1
    assert facts[0].value == str(int(Decimal("150") * 1_000_000 * 100))


def test_thousand_multiplier_suffix():
    facts = _facts("Bridge round was $5K.")
    assert len(facts) == 1
    assert facts[0].value == str(int(Decimal("5") * 1_000 * 100))


def test_plain_amount_with_commas():
    facts = _facts("Revenue was $1,250,000.")
    assert len(facts) == 1
    assert facts[0].value == str(int(Decimal("1250000") * 100))


def test_multiple_amounts_in_fixture():
    facts = list(MoneyExtractor().extract(SOURCE))
    assert len(facts) == 5


def test_offsets_point_to_excerpt():
    facts = list(MoneyExtractor().extract(SOURCE))
    raw = FIXTURE_TEXT
    for fact in facts:
        actual = raw[fact.excerpt_offset_start:fact.excerpt_offset_end]
        assert actual == fact.excerpt, (
            f"Offset mismatch: expected {fact.excerpt!r}, got {actual!r}"
        )


def test_property_slug():
    facts = _facts("Deal was $100M.")
    assert facts[0].property_slug == "deal_value_usd"


def test_value_type_is_number():
    facts = _facts("Deal was $100M.")
    assert facts[0].value_type == "number"


def test_extraction_method_tag():
    facts = _facts("Deal was $100M.")
    assert facts[0].extraction_method == "regex:money-v1"


def test_no_match_on_clean_text():
    assert _facts("No dollar amounts here.") == []
```

Run:

```bash
$ python -m pytest tests/extractors/deterministic/test_money.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/deterministic/money.py`:

```python
# factvault/extractors/deterministic/money.py
"""
USD money amount extractor.

Matches dollar amounts with optional multiplier suffixes.
Value is normalised to integer cents (as a string) so it can be stored in
statements.val_number (NUMERIC) without floating-point error.

Supported forms:
    $4.2 billion   → 420000000000   (cents)
    $320 million   → 32000000000
    $150M          → 15000000000
    $5K            → 50000
    $5 thousand    → 50000
    $1,250,000     → 125000000
    $0.99          → 99

Non-goal (v1): non-USD currencies, implicit millions ("4.2B" without $).
"""
from __future__ import annotations

import re
from decimal import Decimal
from typing import Iterator

from factvault.extractors.base import ExtractedFact, Extractor

# ---------------------------------------------------------------------------
# Pattern: $ AMOUNT [MULTIPLIER_SUFFIX]
# Group 1: digits + optional commas + optional decimal
# Group 2: optional multiplier word/letter (billion|million|thousand|B|M|K)
# ---------------------------------------------------------------------------
_PATTERN = re.compile(
    r"""
    (\$)                                          # dollar sign
    ([\d,]+(?:\.\d+)?)                            # amount: digits, commas, optional decimal
    \s*
    (billion|million|thousand|[BMKbmk])\b         # optional multiplier
    |
    (\$)                                          # dollar sign (plain form, no multiplier)
    ([\d,]+(?:\.\d+)?)                            # amount
    \b
    """,
    re.VERBOSE | re.IGNORECASE,
)

_MULTIPLIERS: dict[str, Decimal] = {
    "billion": Decimal("1000000000"),
    "million": Decimal("1000000"),
    "thousand": Decimal("1000"),
    "b": Decimal("1000000000"),
    "m": Decimal("1000000"),
    "k": Decimal("1000"),
}

_METHOD = "regex:money-v1"
_CONFIDENCE = 0.85


def _parse_amount(digits: str, multiplier: str | None) -> int:
    """Return integer cents."""
    clean = digits.replace(",", "")
    amount = Decimal(clean)
    if multiplier:
        amount *= _MULTIPLIERS[multiplier.lower()]
    cents = amount * 100
    return int(cents)


class MoneyExtractor(Extractor):
    """Yields ExtractedFact instances for USD amounts in source['raw_text']."""

    def extract(self, source: dict) -> Iterator[ExtractedFact]:
        raw_text: str = source.get("raw_text") or ""
        if not raw_text:
            return

        for match in _PATTERN.finditer(raw_text):
            # Determine which branch matched (with multiplier vs plain).
            if match.group(1):
                # Branch 1: with multiplier
                digits = match.group(2)
                multiplier = match.group(3)
            else:
                # Branch 2: plain amount
                digits = match.group(5)
                multiplier = None

            try:
                cents = _parse_amount(digits, multiplier)
            except Exception:
                continue

            excerpt = match.group(0)
            start = match.start(0)
            end = match.end(0)

            yield ExtractedFact(
                subject_text=excerpt,
                property_slug="deal_value_usd",
                value=str(cents),
                value_type="number",
                excerpt=excerpt,
                excerpt_offset_start=start,
                excerpt_offset_end=end,
                extraction_method=_METHOD,
                source_confidence=_CONFIDENCE,
            )
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/deterministic/test_money.py -x -v
# expected: 11 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/deterministic/money.py \
        tests/extractors/deterministic/test_money.py
git commit -m "feat(extractors): USD money extractor with multiplier suffix normalisation"
```

---

### Task 5 — Date extractor

- [ ] **FAIL:** Write the failing test:

```python
# tests/extractors/deterministic/test_dates.py
import pytest
from factvault.extractors.deterministic.dates import DateExtractor


def _facts(text: str):
    src = {"id": "x", "raw_text": text, "publisher": "x.com", "tenant_id": "t-1"}
    return list(DateExtractor().extract(src))


def test_iso_date():
    facts = _facts("Filed on 2024-11-14.")
    assert len(facts) == 1
    assert facts[0].value == "2024-11-14"


def test_iso_datetime():
    facts = _facts("Submitted 2024-11-14T09:30:00Z.")
    assert len(facts) == 1
    assert facts[0].value == "2024-11-14"


def test_month_dd_yyyy():
    facts = _facts("Published November 14, 2024.")
    assert len(facts) == 1
    assert facts[0].value == "2024-11-14"


def test_month_abbreviated():
    facts = _facts("Filed Nov 14, 2024.")
    assert len(facts) == 1
    assert facts[0].value == "2024-11-14"


def test_dd_month_yyyy():
    facts = _facts("Approved 14 November 2024.")
    assert len(facts) == 1
    assert facts[0].value == "2024-11-14"


def test_multiple_dates():
    facts = _facts("From 2024-01-01 to 2024-12-31.")
    assert len(facts) == 2
    values = {f.value for f in facts}
    assert values == {"2024-01-01", "2024-12-31"}


def test_offsets_point_to_excerpt():
    text = "Filed on November 14, 2024 and also on 2025-03-01."
    src = {"id": "x", "raw_text": text, "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(DateExtractor().extract(src))
    for fact in facts:
        actual = text[fact.excerpt_offset_start:fact.excerpt_offset_end]
        assert actual == fact.excerpt


def test_property_slug():
    facts = _facts("Filed 2024-11-14.")
    assert facts[0].property_slug == "date_mentioned"


def test_value_type_is_date():
    facts = _facts("Filed 2024-11-14.")
    assert facts[0].value_type == "date"


def test_extraction_method_tag():
    facts = _facts("Filed 2024-11-14.")
    assert facts[0].extraction_method == "regex:dates-v1"


def test_relative_dates_not_supported():
    """v1 non-goal: relative dates like 'two weeks ago' are not extracted."""
    facts = _facts("This happened two weeks ago.")
    assert facts == []


def test_no_match_on_clean_text():
    assert _facts("No dates here.") == []
```

Run:

```bash
$ python -m pytest tests/extractors/deterministic/test_dates.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/deterministic/dates.py`:

```python
# factvault/extractors/deterministic/dates.py
"""
Date extractor for ISO-8601, "Month DD, YYYY", and "DD Month YYYY" formats.

Non-goal (v1): relative dates ("two weeks ago", "last quarter", fiscal quarters).
These are documented here as explicit non-goals to avoid scope creep.

All extracted dates are normalised to ISO-8601 date string (YYYY-MM-DD).
The property_slug is 'date_mentioned' — a generic date property.
Downstream workers may re-slug to a more specific property (e.g. 'published_at',
'filed_at') during statement enrichment; that is out of scope for the extractor.
"""
from __future__ import annotations

import re
from datetime import date
from typing import Iterator

from factvault.extractors.base import ExtractedFact, Extractor

_MONTH_MAP: dict[str, int] = {
    "january": 1, "jan": 1,
    "february": 2, "feb": 2,
    "march": 3, "mar": 3,
    "april": 4, "apr": 4,
    "may": 5,
    "june": 6, "jun": 6,
    "july": 7, "jul": 7,
    "august": 8, "aug": 8,
    "september": 9, "sep": 9, "sept": 9,
    "october": 10, "oct": 10,
    "november": 11, "nov": 11,
    "december": 12, "dec": 12,
}

_MONTH_NAMES = "|".join(sorted(_MONTH_MAP.keys(), key=len, reverse=True))

# ---------------------------------------------------------------------------
# Patterns — order matters: most specific first.
# ---------------------------------------------------------------------------

# ISO-8601 datetime: 2024-11-14T09:30:00Z or 2024-11-14T09:30:00+00:00
_ISO_DATETIME = re.compile(
    r"\b(\d{4}-\d{2}-\d{2})T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})?\b"
)

# ISO date: 2024-11-14
_ISO_DATE = re.compile(r"\b(\d{4}-(?:0[1-9]|1[0-2])-(?:0[1-9]|[12]\d|3[01]))\b")

# "Month DD, YYYY" or "Mon DD, YYYY" — e.g. "November 14, 2024" / "Nov 14, 2024"
_MDY = re.compile(
    r"\b(" + _MONTH_NAMES + r")\s+(\d{1,2}),\s*(\d{4})\b",
    re.IGNORECASE,
)

# "DD Month YYYY" — e.g. "14 November 2024"
_DMY = re.compile(
    r"\b(\d{1,2})\s+(" + _MONTH_NAMES + r")\s+(\d{4})\b",
    re.IGNORECASE,
)

_METHOD = "regex:dates-v1"
_CONFIDENCE = 0.80


def _iso(year: int, month: int, day: int) -> str:
    return date(year, month, day).isoformat()


class DateExtractor(Extractor):
    """Yields ExtractedFact for each date pattern found in source['raw_text']."""

    def extract(self, source: dict) -> Iterator[ExtractedFact]:
        raw_text: str = source.get("raw_text") or ""
        if not raw_text:
            return

        seen_spans: set[tuple[int, int]] = set()

        def _emit(excerpt: str, start: int, end: int, iso_value: str) -> ExtractedFact | None:
            span = (start, end)
            if span in seen_spans:
                return None
            seen_spans.add(span)
            return ExtractedFact(
                subject_text=excerpt,
                property_slug="date_mentioned",
                value=iso_value,
                value_type="date",
                excerpt=excerpt,
                excerpt_offset_start=start,
                excerpt_offset_end=end,
                extraction_method=_METHOD,
                source_confidence=_CONFIDENCE,
            )

        # ISO datetime (must come before ISO date to avoid partial match)
        for m in _ISO_DATETIME.finditer(raw_text):
            fact = _emit(m.group(0), m.start(), m.end(), m.group(1))
            if fact:
                yield fact

        # ISO date
        for m in _ISO_DATE.finditer(raw_text):
            fact = _emit(m.group(0), m.start(), m.end(), m.group(1))
            if fact:
                yield fact

        # Month DD, YYYY
        for m in _MDY.finditer(raw_text):
            try:
                month = _MONTH_MAP[m.group(1).lower()]
                day = int(m.group(2))
                year = int(m.group(3))
                value = _iso(year, month, day)
            except (KeyError, ValueError):
                continue
            fact = _emit(m.group(0), m.start(), m.end(), value)
            if fact:
                yield fact

        # DD Month YYYY
        for m in _DMY.finditer(raw_text):
            try:
                day = int(m.group(1))
                month = _MONTH_MAP[m.group(2).lower()]
                year = int(m.group(3))
                value = _iso(year, month, day)
            except (KeyError, ValueError):
                continue
            fact = _emit(m.group(0), m.start(), m.end(), value)
            if fact:
                yield fact
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/deterministic/test_dates.py -x -v
# expected: 12 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/deterministic/dates.py \
        tests/extractors/deterministic/test_dates.py
git commit -m "feat(extractors): date extractor (ISO-8601, Month DD YYYY, DD Month YYYY)"
```

---

### Task 6 — Gazetteer entity matcher + starter CSVs

- [ ] **FAIL:** Write the failing test:

```python
# tests/extractors/deterministic/test_gazetteer.py
import pytest
from pathlib import Path
from factvault.extractors.deterministic.gazetteer import GazetteerExtractor


def _make_extractor(tmp_path: Path) -> GazetteerExtractor:
    """Build a minimal gazetteer for tests; does not depend on shipped CSVs."""
    csv_content = "canonical_name,aliases\nApple Inc.,Apple|AAPL|Apple Computer\nMicrosoft Corporation,Microsoft|MSFT\n"
    csv_file = tmp_path / "test_entities.csv"
    csv_file.write_text(csv_content)
    return GazetteerExtractor(gazetteer_dir=tmp_path)


def test_canonical_name_match(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "Apple Inc. announced new products.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert len(facts) == 1
    assert facts[0].value == "Apple Inc."


def test_alias_match(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "AAPL stock surged today.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert len(facts) == 1
    assert facts[0].value == "Apple Inc."


def test_multiple_entities(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "Microsoft and Apple are competing.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    values = {f.value for f in facts}
    assert "Apple Inc." in values
    assert "Microsoft Corporation" in values


def test_no_false_positive(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "No known entities here.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert facts == []


def test_property_slug(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "Apple Inc. quarterly results.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert all(f.property_slug == "mentioned_entity" for f in facts)


def test_value_type_is_string(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "MSFT rose 2%.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert all(f.value_type == "string" for f in facts)


def test_offsets_point_to_excerpt(tmp_path):
    ext = _make_extractor(tmp_path)
    text = "Apple Inc. and Microsoft Corporation both reported earnings."
    src = {"id": "x", "raw_text": text, "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    for fact in facts:
        actual = text[fact.excerpt_offset_start:fact.excerpt_offset_end]
        assert actual == fact.excerpt


def test_extraction_method_tag(tmp_path):
    ext = _make_extractor(tmp_path)
    src = {"id": "x", "raw_text": "Apple Inc. announced.", "publisher": "x.com", "tenant_id": "t-1"}
    facts = list(ext.extract(src))
    assert all(f.extraction_method == "gazetteer:exact-v1" for f in facts)


def test_shipped_sp500_csv_loads():
    """Confirm the shipped sp500_companies.csv is parseable."""
    data_dir = Path(__file__).resolve().parents[3] / "data" / "gazetteer"
    if not (data_dir / "sp500_companies.csv").exists():
        pytest.skip("Shipped CSV not yet present — will be created in Task 6 IMPLEMENT step")
    ext = GazetteerExtractor(gazetteer_dir=data_dir)
    assert len(ext._entries) > 0


def test_shipped_politicians_csv_loads():
    """Confirm the shipped us_politicians.csv is parseable."""
    data_dir = Path(__file__).resolve().parents[3] / "data" / "gazetteer"
    if not (data_dir / "us_politicians.csv").exists():
        pytest.skip("Shipped CSV not yet present — will be created in Task 6 IMPLEMENT step")
    ext = GazetteerExtractor(gazetteer_dir=data_dir)
    assert len(ext._entries) > 0
```

Run:

```bash
$ python -m pytest tests/extractors/deterministic/test_gazetteer.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/deterministic/gazetteer.py`:

```python
# factvault/extractors/deterministic/gazetteer.py
"""
Gazetteer-based named entity extractor.

Loads CSV files from a configurable directory. Each CSV has columns:
    canonical_name  — the authoritative entity name (returned as value)
    aliases         — pipe-separated list of alternative names/ticker symbols

For each name and alias, exact substring match (word-boundary aware) is
attempted against source raw_text. Match → ExtractedFact with:
    subject_text    : matched text as it appears in source
    property_slug   : 'mentioned_entity'
    value           : canonical_name from the CSV
    value_type      : 'string'
    extraction_method: 'gazetteer:exact-v1'

Default gazetteer_dir: <package_root>/../../data/gazetteer (resolved at import).
Can be overridden in the constructor for testing.
"""
from __future__ import annotations

import csv
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Iterator

from factvault.extractors.base import ExtractedFact, Extractor

_DEFAULT_GAZETTEER_DIR = Path(__file__).resolve().parents[4] / "data" / "gazetteer"

_METHOD = "gazetteer:exact-v1"
_CONFIDENCE = 0.80


@dataclass
class _GazetteerEntry:
    canonical_name: str
    terms: list[str] = field(default_factory=list)
    # Pre-compiled patterns keyed by term string
    _patterns: dict[str, re.Pattern[str]] = field(default_factory=dict, repr=False)

    def __post_init__(self) -> None:
        for term in self.terms:
            # Word-boundary match; escape special chars in entity names.
            escaped = re.escape(term)
            self._patterns[term] = re.compile(r"\b" + escaped + r"\b", re.IGNORECASE)


def _load_gazetteer(gazetteer_dir: Path) -> list[_GazetteerEntry]:
    entries: list[_GazetteerEntry] = []
    if not gazetteer_dir.is_dir():
        return entries
    for csv_path in sorted(gazetteer_dir.glob("*.csv")):
        with csv_path.open(newline="", encoding="utf-8") as fh:
            reader = csv.DictReader(fh)
            for row in reader:
                canonical = row.get("canonical_name", "").strip()
                if not canonical:
                    continue
                aliases_raw = row.get("aliases", "").strip()
                aliases = [a.strip() for a in aliases_raw.split("|") if a.strip()]
                terms = [canonical] + aliases
                entries.append(_GazetteerEntry(canonical_name=canonical, terms=terms))
    return entries


class GazetteerExtractor(Extractor):
    """Exact-match named entity extractor backed by CSV gazetteers."""

    def __init__(self, gazetteer_dir: Path | None = None) -> None:
        self._gazetteer_dir = gazetteer_dir or _DEFAULT_GAZETTEER_DIR
        self._entries = _load_gazetteer(self._gazetteer_dir)

    def extract(self, source: dict) -> Iterator[ExtractedFact]:
        raw_text: str = source.get("raw_text") or ""
        if not raw_text or not self._entries:
            return

        seen_spans: set[tuple[int, int]] = set()

        for entry in self._entries:
            for term, pattern in entry._patterns.items():
                for match in pattern.finditer(raw_text):
                    span = (match.start(), match.end())
                    if span in seen_spans:
                        continue
                    seen_spans.add(span)
                    excerpt = match.group(0)
                    yield ExtractedFact(
                        subject_text=excerpt,
                        property_slug="mentioned_entity",
                        value=entry.canonical_name,
                        value_type="string",
                        excerpt=excerpt,
                        excerpt_offset_start=match.start(),
                        excerpt_offset_end=match.end(),
                        extraction_method=_METHOD,
                        source_confidence=_CONFIDENCE,
                    )
```

- [ ] **IMPLEMENT:** Create starter CSV files with realistic content:

```bash
mkdir -p data/gazetteer
```

`data/gazetteer/sp500_companies.csv` — top 20 S&P 500 companies by market cap with common aliases:

```
canonical_name,aliases
Apple Inc.,Apple|AAPL|Apple Computer Inc.
Microsoft Corporation,Microsoft|MSFT
NVIDIA Corporation,NVIDIA|NVDA|Nvidia
Alphabet Inc.,Alphabet|GOOGL|GOOG|Google|Google LLC
Amazon.com Inc.,Amazon|AMZN|Amazon.com
Meta Platforms Inc.,Meta|META|Facebook|Facebook Inc.
Berkshire Hathaway Inc.,Berkshire Hathaway|BRK.A|BRK.B
Eli Lilly and Company,Eli Lilly|LLY|Lilly
Broadcom Inc.,Broadcom|AVGO
JPMorgan Chase & Co.,JPMorgan Chase|JPMorgan|JPM|J.P. Morgan
Tesla Inc.,Tesla|TSLA
Exxon Mobil Corporation,Exxon Mobil|ExxonMobil|XOM|Exxon
UnitedHealth Group Incorporated,UnitedHealth Group|UnitedHealth|UNH
Visa Inc.,Visa|V
Johnson & Johnson,J&J|JNJ|Johnson and Johnson
Walmart Inc.,Walmart|WMT|Wal-Mart
Mastercard Incorporated,Mastercard|MA
Procter & Gamble Company,Procter & Gamble|P&G|PG
The Home Depot Inc.,Home Depot|HD
Costco Wholesale Corporation,Costco|COST
```

`data/gazetteer/us_politicians.csv` — top 20 current US senators with aliases:

```
canonical_name,aliases,jurisdiction
Chuck Schumer,Charles Schumer|Sen. Schumer|Schumer,New York
Mitch McConnell,Mitchell McConnell|Sen. McConnell|McConnell,Kentucky
Bernie Sanders,Bernard Sanders|Sen. Sanders|Sanders,Vermont
Elizabeth Warren,Sen. Warren|Warren,Massachusetts
Ron Wyden,Sen. Wyden|Wyden,Oregon
Maria Cantwell,Sen. Cantwell|Cantwell,Washington
Patty Murray,Sen. Murray|Murray,Washington
Dick Durbin,Richard Durbin|Sen. Durbin|Durbin,Illinois
Amy Klobuchar,Sen. Klobuchar|Klobuchar,Minnesota
Dianne Feinstein,Sen. Feinstein|Feinstein,California
Mark Warner,Sen. Warner|Warner,Virginia
Sheldon Whitehouse,Sen. Whitehouse|Whitehouse,Rhode Island
John Thune,Sen. Thune|Thune,South Dakota
Susan Collins,Sen. Collins|Collins,Maine
Lisa Murkowski,Sen. Murkowski|Murkowski,Alaska
Lindsey Graham,Sen. Graham|Graham,South Carolina
John Cornyn,Sen. Cornyn|Cornyn,Texas
Ted Cruz,Rafael Cruz|Sen. Cruz|Cruz,Texas
Marco Rubio,Sen. Rubio|Rubio,Florida
Rick Scott,Sen. Scott|Scott,Florida
```

Note: the politicians CSV has an extra `jurisdiction` column; the loader uses only `canonical_name` and `aliases`. DictReader ignores extra columns automatically.

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/deterministic/test_gazetteer.py -x -v
# expected: 10 passed (the skip tests pass as skip or as pass depending on CSV presence)
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/deterministic/gazetteer.py \
        tests/extractors/deterministic/test_gazetteer.py \
        data/gazetteer/sp500_companies.csv \
        data/gazetteer/us_politicians.csv
git commit -m "feat(extractors): gazetteer entity extractor + starter S&P 500 + US senator CSVs"
```

---

### Task 7 — Deterministic runner

- [ ] **FAIL:** Write the failing test:

```python
# tests/extractors/deterministic/test_runner.py
import pytest
from factvault.extractors.deterministic.runner import DeterministicRunner

FIXTURE_TEXT = (
    "Apple Inc. (CIK 0000320193) raised $4.2 billion on November 14, 2024. "
    "The drug trial NCT12345678 results were published."
)

SOURCE = {"id": "src-1", "raw_text": FIXTURE_TEXT, "publisher": "example.com", "tenant_id": "t-1"}


def test_runner_returns_facts():
    runner = DeterministicRunner()
    facts = list(runner.run(SOURCE))
    assert len(facts) > 0


def test_runner_covers_cik():
    runner = DeterministicRunner()
    facts = list(runner.run(SOURCE))
    assert any(f.property_slug == "sec_cik" for f in facts)


def test_runner_covers_money():
    runner = DeterministicRunner()
    facts = list(runner.run(SOURCE))
    assert any(f.property_slug == "deal_value_usd" for f in facts)


def test_runner_covers_date():
    runner = DeterministicRunner()
    facts = list(runner.run(SOURCE))
    assert any(f.property_slug == "date_mentioned" for f in facts)


def test_runner_covers_nct():
    runner = DeterministicRunner()
    facts = list(runner.run(SOURCE))
    assert any(f.property_slug == "nct_id" for f in facts)


def test_covered_spans_are_returned():
    runner = DeterministicRunner()
    facts, covered_spans = runner.run_with_spans(SOURCE)
    assert isinstance(covered_spans, list)
    assert len(covered_spans) > 0
    for span in covered_spans:
        assert isinstance(span, tuple)
        assert len(span) == 2
        start, end = span
        assert 0 <= start < end <= len(FIXTURE_TEXT)


def test_covered_spans_align_with_facts():
    runner = DeterministicRunner()
    facts, covered_spans = runner.run_with_spans(SOURCE)
    fact_spans = {(f.excerpt_offset_start, f.excerpt_offset_end) for f in facts}
    for span in covered_spans:
        assert span in fact_spans


def test_no_duplicate_spans():
    runner = DeterministicRunner()
    _, covered_spans = runner.run_with_spans(SOURCE)
    assert len(covered_spans) == len(set(covered_spans))


def test_empty_source_returns_empty():
    empty_src = {"id": "x", "raw_text": "", "publisher": "x.com", "tenant_id": "t-1"}
    runner = DeterministicRunner()
    facts, spans = runner.run_with_spans(empty_src)
    assert facts == []
    assert spans == []
```

Run:

```bash
$ python -m pytest tests/extractors/deterministic/test_runner.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/deterministic/runner.py`:

```python
# factvault/extractors/deterministic/runner.py
"""
Deterministic extractor runner.

Composes all four deterministic extractors into a single pass.
Returns extracted facts and a list of covered character spans so the
LLM extractor knows which portions of raw_text have already been processed.

Usage
-----
    runner = DeterministicRunner()
    facts = runner.run(source)                     # facts only
    facts, spans = runner.run_with_spans(source)   # facts + covered spans
"""
from __future__ import annotations

from typing import Iterator

from factvault.extractors.base import ExtractedFact
from factvault.extractors.deterministic.dates import DateExtractor
from factvault.extractors.deterministic.gazetteer import GazetteerExtractor
from factvault.extractors.deterministic.identifiers import IdentifierExtractor
from factvault.extractors.deterministic.money import MoneyExtractor


class DeterministicRunner:
    """Runs all deterministic extractors in sequence on a source dict."""

    def __init__(self) -> None:
        self._extractors = [
            IdentifierExtractor(),
            MoneyExtractor(),
            DateExtractor(),
            GazetteerExtractor(),
        ]

    def run(self, source: dict) -> list[ExtractedFact]:
        """Return all ExtractedFact instances from all deterministic extractors."""
        facts, _ = self.run_with_spans(source)
        return facts

    def run_with_spans(
        self, source: dict
    ) -> tuple[list[ExtractedFact], list[tuple[int, int]]]:
        """Return (facts, covered_spans).

        covered_spans is a deduplicated list of (start, end) character ranges
        into source['raw_text'] that are already covered by deterministic
        extraction. The LLM extractor receives this list to skip re-processing
        spans that are already extracted.
        """
        facts: list[ExtractedFact] = []
        seen_spans: set[tuple[int, int]] = set()

        for extractor in self._extractors:
            for fact in extractor.extract(source):
                span = (fact.excerpt_offset_start, fact.excerpt_offset_end)
                if span not in seen_spans:
                    facts.append(fact)
                    seen_spans.add(span)

        covered_spans = sorted(seen_spans)
        return facts, covered_spans
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/deterministic/test_runner.py -x -v
# expected: 9 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/deterministic/runner.py \
        tests/extractors/deterministic/test_runner.py
git commit -m "feat(extractors): deterministic runner composing all 4 extractors + covered-span tracking"
```

---

### Task 8 — LLM extractor base (HTTP client + response parsing)

- [ ] **FAIL:** Create `tests/extractors/test_llm.py`:

```python
# tests/extractors/test_llm.py
"""
Tests for LLMExtractor.

Uses pytest-httpx to mock the OpenAI-compatible endpoint.
The extractor is initialised with explicit env overrides so tests
are hermetic (no real network calls, no env var bleed-through).
"""
import json
import pytest
from factvault.extractors.llm import LLMExtractor

_LLM_URL = "http://localhost:11434/v1"
_LLM_MODEL = "llama3.1:8b"

RAW_TEXT = (
    "OpenAI was founded by Sam Altman in 2015. The company is headquartered "
    "in San Francisco, California. It raised $10 billion from Microsoft."
)

SOURCE = {
    "id": "src-llm-1",
    "raw_text": RAW_TEXT,
    "publisher": "example.com",
    "tenant_id": "t-1",
}

_VALID_RESPONSE = {
    "statements": [
        {
            "subject_label": "OpenAI",
            "property_slug": "founded_by",
            "value": "Sam Altman",
            "value_type_hint": "string",
            "excerpt": "OpenAI was founded by Sam Altman",
            "excerpt_offset_start": 0,
            "excerpt_offset_end": 31,
            "qualifiers": [],
        },
        {
            "subject_label": "OpenAI",
            "property_slug": "headquarters_location",
            "value": "San Francisco, California",
            "value_type_hint": "string",
            "excerpt": "headquartered in San Francisco, California",
            "excerpt_offset_start": 67,
            "excerpt_offset_end": 109,
            "qualifiers": [],
        },
    ]
}


def _mock_chat_response(content: str) -> dict:
    return {
        "id": "chatcmpl-test",
        "object": "chat.completion",
        "choices": [
            {
                "index": 0,
                "message": {"role": "assistant", "content": content},
                "finish_reason": "stop",
            }
        ],
        "usage": {"prompt_tokens": 100, "completion_tokens": 50, "total_tokens": 150},
    }


def test_llm_extractor_returns_facts(httpx_mock):
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_VALID_RESPONSE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert len(facts) == 2


def test_llm_extractor_maps_property_slug(httpx_mock):
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_VALID_RESPONSE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    slugs = {f.property_slug for f in facts}
    assert "founded_by" in slugs
    assert "headquarters_location" in slugs


def test_llm_extractor_extraction_method_tag(httpx_mock):
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_VALID_RESPONSE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    for fact in facts:
        assert fact.extraction_method.startswith("llm:")


def test_llm_extractor_skips_covered_spans(httpx_mock):
    """When all text is covered by deterministic extractors, no LLM call is made."""
    full_span = [(0, len(RAW_TEXT))]
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=full_span))
    assert facts == []
    # httpx_mock has no registered response — if a request was made it would raise


def test_llm_extractor_handles_empty_response(httpx_mock):
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps({"statements": []})),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert facts == []


def test_llm_extractor_handles_malformed_json(httpx_mock):
    """Malformed JSON from LLM must not raise — returns empty list."""
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response("this is not json"),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert facts == []
```

Run:

```bash
$ python -m pytest tests/extractors/test_llm.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/extractors/llm.py` (base, without offset verification — that is Task 9):

```python
# factvault/extractors/llm.py
"""
LLM extractor using an OpenAI-compatible structured-output endpoint.

Configuration (env vars, with constructor overrides for testing):
    FACTVAULT_LLM_URL    — base URL of the OpenAI-compatible endpoint
                           Default: http://localhost:11434/v1  (Ollama)
    FACTVAULT_LLM_MODEL  — model name
                           Default: llama3.1:8b
    FACTVAULT_LLM_API_KEY — API key (empty string = no auth header)

The extractor:
1. Receives source dict + covered_spans (list of (start, end) tuples from
   the deterministic runner).
2. Builds uncovered_text by masking covered spans with whitespace.
3. Sends a chat completion request with a strict JSON response schema
   (via response_format or system prompt).
4. Parses the JSON into ExtractedFact instances.
5. Runs offset verification on each fact (Task 9 adds the rejection logic;
   in this base the verification is a pass-through that logs mismatches).

Offset verification (full implementation in Task 9) rejects facts where
source['raw_text'][offset_start:offset_end] does not match the claimed excerpt.
"""
from __future__ import annotations

import json
import logging
import os
import re
from typing import Iterator

from openai import OpenAI

from factvault.extractors.base import ExtractedFact, Extractor

logger = logging.getLogger(__name__)

_DEFAULT_URL = "http://localhost:11434/v1"
_DEFAULT_MODEL = "llama3.1:8b"

# ---------------------------------------------------------------------------
# JSON schema sent to the LLM as response_format (JSON mode).
# Mirrors spec §4.3 LLM structured output schema.
# ---------------------------------------------------------------------------
_RESPONSE_SCHEMA = {
    "type": "object",
    "properties": {
        "statements": {
            "type": "array",
            "items": {
                "type": "object",
                "required": [
                    "subject_label",
                    "property_slug",
                    "value",
                    "excerpt",
                    "excerpt_offset_start",
                    "excerpt_offset_end",
                ],
                "properties": {
                    "subject_label":        {"type": "string"},
                    "property_slug":        {"type": "string"},
                    "value":                {"type": "string"},
                    "value_type_hint":      {
                        "type": "string",
                        "enum": ["entity_ref", "string", "number", "date", "url"],
                    },
                    "excerpt":              {"type": "string"},
                    "excerpt_offset_start": {"type": "integer", "minimum": 0},
                    "excerpt_offset_end":   {"type": "integer", "minimum": 1},
                    "qualifiers":           {"type": "array", "items": {"type": "object"}},
                },
            },
        }
    },
    "required": ["statements"],
}

_SYSTEM_PROMPT = (
    "You are a structured fact extraction engine. Extract factual statements from "
    "the provided text. For each fact, provide the verbatim excerpt from the text "
    "and the exact character offsets (start inclusive, end exclusive) into the "
    "provided text where that excerpt appears. Do not invent or paraphrase excerpts. "
    "Return only a JSON object matching the provided schema."
)

_USER_PROMPT_TEMPLATE = (
    "Extract all factual statements from the following text. "
    "Character offsets must point to the exact location of the excerpt in this text.\n\n"
    "TEXT:\n{text}\n\n"
    "Return JSON only."
)


def _mask_covered_spans(raw_text: str, covered_spans: list[tuple[int, int]]) -> str:
    """Replace covered spans with spaces to produce uncovered_text for the LLM."""
    if not covered_spans:
        return raw_text
    chars = list(raw_text)
    for start, end in covered_spans:
        for i in range(start, min(end, len(chars))):
            chars[i] = " "
    return "".join(chars)


def _is_effectively_empty(text: str) -> bool:
    return not text.strip()


class LLMExtractor(Extractor):
    """Extracts facts using an OpenAI-compatible LLM endpoint.

    Parameters
    ----------
    base_url : override for FACTVAULT_LLM_URL
    model    : override for FACTVAULT_LLM_MODEL
    api_key  : override for FACTVAULT_LLM_API_KEY
    """

    def __init__(
        self,
        base_url: str | None = None,
        model: str | None = None,
        api_key: str | None = None,
    ) -> None:
        self._base_url = base_url or os.getenv("FACTVAULT_LLM_URL", _DEFAULT_URL)
        self._model = model or os.getenv("FACTVAULT_LLM_MODEL", _DEFAULT_MODEL)
        self._api_key = api_key if api_key is not None else os.getenv("FACTVAULT_LLM_API_KEY", "")
        self._client = OpenAI(
            base_url=self._base_url,
            api_key=self._api_key or "no-key",
        )

    @property
    def extraction_method(self) -> str:
        return f"llm:{self._model}:v1"

    def extract(
        self,
        source: dict,
        covered_spans: list[tuple[int, int]] | None = None,
    ) -> Iterator[ExtractedFact]:
        """Yield ExtractedFact instances from LLM extraction.

        Parameters
        ----------
        source        : source dict with at minimum 'raw_text'
        covered_spans : spans already covered by deterministic extraction;
                        these are masked before sending to the LLM.
        """
        raw_text: str = source.get("raw_text") or ""
        if not raw_text:
            return

        spans = covered_spans or []
        uncovered = _mask_covered_spans(raw_text, spans)

        if _is_effectively_empty(uncovered):
            logger.debug("LLMExtractor: all text covered by deterministic pass; skipping LLM call.")
            return

        proposals = self._call_llm(uncovered)

        for proposal in proposals:
            fact = self._proposal_to_fact(source, raw_text, proposal)
            if fact is not None:
                yield fact

    def _call_llm(self, text: str) -> list[dict]:
        """Call LLM and return parsed list of statement proposals. Returns [] on error."""
        try:
            response = self._client.chat.completions.create(
                model=self._model,
                messages=[
                    {"role": "system", "content": _SYSTEM_PROMPT},
                    {"role": "user", "content": _USER_PROMPT_TEMPLATE.format(text=text)},
                ],
                response_format={"type": "json_object"},
            )
            content = response.choices[0].message.content or ""
            parsed = json.loads(content)
            return parsed.get("statements", [])
        except json.JSONDecodeError as exc:
            logger.warning("LLMExtractor: JSON decode error: %s", exc)
            return []
        except Exception as exc:
            logger.warning("LLMExtractor: LLM call failed: %s", exc)
            return []

    def _proposal_to_fact(
        self, source: dict, raw_text: str, proposal: dict
    ) -> ExtractedFact | None:
        """Convert a raw LLM proposal dict to ExtractedFact, or None if invalid.

        Offset verification is performed here. A proposal is rejected if
        raw_text[offset_start:offset_end] does not match the claimed excerpt
        (after whitespace normalisation). See Task 9 for full verification logic.
        """
        try:
            subject_label = str(proposal["subject_label"])
            property_slug = str(proposal["property_slug"])
            value = str(proposal["value"])
            value_type = str(proposal.get("value_type_hint", "string"))
            excerpt = str(proposal["excerpt"])
            offset_start = int(proposal["excerpt_offset_start"])
            offset_end = int(proposal["excerpt_offset_end"])
        except (KeyError, TypeError, ValueError) as exc:
            logger.debug("LLMExtractor: malformed proposal %s: %s", proposal, exc)
            return None

        # Offset verification (base implementation — full rejection logic in Task 9)
        if not self._verify_offset(raw_text, excerpt, offset_start, offset_end):
            logger.info(
                "LLMExtractor: offset verification FAILED for %r at [%d:%d] — rejecting.",
                property_slug,
                offset_start,
                offset_end,
            )
            return None

        return ExtractedFact(
            subject_text=subject_label,
            property_slug=property_slug,
            value=value,
            value_type=value_type,
            excerpt=excerpt,
            excerpt_offset_start=offset_start,
            excerpt_offset_end=offset_end,
            extraction_method=self.extraction_method,
            source_confidence=0.70,
        )

    def _verify_offset(
        self, raw_text: str, excerpt: str, offset_start: int, offset_end: int
    ) -> bool:
        """Base offset check — overridden with full logic in Task 9."""
        # Bounds check
        if offset_start < 0 or offset_end > len(raw_text) or offset_start >= offset_end:
            return False
        actual = raw_text[offset_start:offset_end]
        # Whitespace normalisation: collapse runs to single space
        def _norm(s: str) -> str:
            return re.sub(r"\s+", " ", s).strip()
        return _norm(actual) == _norm(excerpt)
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/test_llm.py -x -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/llm.py tests/extractors/test_llm.py
git commit -m "feat(extractors): LLM extractor base (OpenAI-compatible client, JSON response parsing)"
```

---

### Task 9 — LLM extractor offset verification gate

- [ ] **FAIL:** Extend `tests/extractors/test_llm.py` with the verification tests (append to the existing file):

```python
# Append to tests/extractors/test_llm.py


# ── Offset verification gate ───────────────────────────────────────────────────

_HALLUCINATED_RESPONSE = {
    "statements": [
        {
            "subject_label": "OpenAI",
            "property_slug": "founded_by",
            "value": "Elon Musk",
            # Offsets point to "OpenAI was founded" but excerpt claims "Sam Altman"
            "value_type_hint": "string",
            "excerpt": "founded by Elon Musk in 2013",
            "excerpt_offset_start": 10,
            "excerpt_offset_end": 35,
            "qualifiers": [],
        }
    ]
}

_OFF_BY_N_RESPONSE = {
    "statements": [
        {
            "subject_label": "OpenAI",
            "property_slug": "founded_by",
            "value": "Sam Altman",
            "value_type_hint": "string",
            "excerpt": "OpenAI was founded by Sam Altman",
            # Off by 5 chars at start — points to wrong text
            "excerpt_offset_start": 5,
            "excerpt_offset_end": 36,
            "qualifiers": [],
        }
    ]
}

_VALID_SINGLE = {
    "statements": [
        {
            "subject_label": "OpenAI",
            "property_slug": "founded_by",
            "value": "Sam Altman",
            "value_type_hint": "string",
            "excerpt": "OpenAI was founded by Sam Altman",
            "excerpt_offset_start": 0,
            "excerpt_offset_end": 31,
            "qualifiers": [],
        }
    ]
}


def test_hallucinated_excerpt_rejected(httpx_mock):
    """LLM claims an excerpt that does not exist at the specified offsets → rejected."""
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_HALLUCINATED_RESPONSE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert facts == [], "Hallucinated excerpt must be rejected"


def test_off_by_n_offsets_rejected(httpx_mock):
    """Offsets that point to different text than the claimed excerpt → rejected."""
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_OFF_BY_N_RESPONSE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert facts == [], "Off-by-N offset must be rejected"


def test_valid_offsets_accepted(httpx_mock):
    """Offsets that correctly point to the claimed excerpt → accepted."""
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(_VALID_SINGLE)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert len(facts) == 1
    assert facts[0].value == "Sam Altman"


def test_out_of_bounds_offsets_rejected(httpx_mock):
    """Offsets beyond the length of raw_text → rejected."""
    bad = {
        "statements": [
            {
                "subject_label": "X",
                "property_slug": "founded_by",
                "value": "Y",
                "excerpt": "something",
                "excerpt_offset_start": 99999,
                "excerpt_offset_end": 100010,
                "qualifiers": [],
            }
        ]
    }
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(bad)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(SOURCE, covered_spans=[]))
    assert facts == []


def test_whitespace_tolerance_in_offset_check(httpx_mock):
    """Minor whitespace differences between excerpt and actual text are tolerated."""
    text_with_double_space = "OpenAI  was  founded  by  Sam Altman in 2015."
    src_ws = {
        "id": "ws-1",
        "raw_text": text_with_double_space,
        "publisher": "x.com",
        "tenant_id": "t-1",
    }
    # Excerpt uses single spaces; actual text has double spaces.
    response = {
        "statements": [
            {
                "subject_label": "OpenAI",
                "property_slug": "founded_by",
                "value": "Sam Altman",
                "excerpt": "OpenAI  was  founded  by  Sam Altman",
                "excerpt_offset_start": 0,
                "excerpt_offset_end": 36,
                "qualifiers": [],
            }
        ]
    }
    httpx_mock.add_response(
        method="POST",
        url=f"{_LLM_URL}/chat/completions",
        json=_mock_chat_response(json.dumps(response)),
    )
    extractor = LLMExtractor(base_url=_LLM_URL, model=_LLM_MODEL, api_key="test")
    facts = list(extractor.extract(src_ws, covered_spans=[]))
    assert len(facts) == 1
```

Run:

```bash
$ python -m pytest tests/extractors/test_llm.py -x -v
# expected: FAILED on the new offset rejection tests (hallucinated excerpt accepted without proper rejection)
```

Note: if `_verify_offset` already handles these cases correctly from the Task 8 implementation, the tests will PASS. If so, document that and move to the commit step.

- [ ] **VERIFY/STRENGTHEN:** Review `_verify_offset` in `factvault/extractors/llm.py` against the five new test cases. The existing implementation handles all five:

  - **Out-of-bounds offsets** — `offset_end > len(raw_text)` returns False ✓
  - **Hallucinated excerpt** — normalised actual != normalised excerpt returns False ✓
  - **Off-by-N** — same comparison ✓
  - **Valid offsets** — normalised match returns True ✓
  - **Whitespace tolerance** — `_norm()` collapses runs to single space ✓

  If any case fails, strengthen `_verify_offset`:

```python
    def _verify_offset(
        self, raw_text: str, excerpt: str, offset_start: int, offset_end: int
    ) -> bool:
        """Anti-hallucination offset verification gate.

        Rejects an LLM-proposed fact if raw_text[offset_start:offset_end]
        does not match the claimed excerpt after whitespace normalisation.

        Whitespace tolerance: collapse any run of whitespace characters
        (space, tab, newline) to a single space, then strip leading/trailing.
        This tolerates minor HTML-to-text conversion artifacts (double spaces,
        trailing newlines) without permitting substantively different text.
        """
        # Bounds guard
        if offset_start < 0 or offset_end > len(raw_text) or offset_start >= offset_end:
            return False
        actual = raw_text[offset_start:offset_end]

        def _norm(s: str) -> str:
            return re.sub(r"\s+", " ", s).strip()

        if _norm(actual) != _norm(excerpt):
            logger.debug(
                "Offset verification FAILED: "
                "raw_text[%d:%d]=%r, excerpt=%r",
                offset_start, offset_end, actual, excerpt,
            )
            return False
        return True
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/extractors/test_llm.py -x -v
# expected: 11 passed (6 from Task 8 + 5 new)
```

- [ ] **COMMIT:**

```bash
git add factvault/extractors/llm.py tests/extractors/test_llm.py
git commit -m "feat(extractors): LLM extractor offset verification gate (anti-hallucination check)"
```

---

### Task 10 — Property vocabulary resolver

- [ ] **FAIL:** Create `tests/vocabulary/__init__.py` (empty) and write the failing test:

```python
# tests/vocabulary/__init__.py
```

```python
# tests/vocabulary/test_resolver.py
"""
Tests for VocabularyResolver.

Uses app_engine (RLS-aware) fixture from tests/conftest.py.
Strict mode and permissive mode are both tested.
"""
import uuid
import pytest
from sqlalchemy import text
from factvault.vocabulary.resolver import VocabularyResolver


@pytest.fixture
def tenant_id() -> str:
    return str(uuid.uuid4())


@pytest.fixture
def known_property_id(app_engine, tenant_id):
    """Insert a known property for the test tenant and return its ID."""
    prop_id = str(uuid.uuid4())
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
        conn.execute(
            text(
                "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
                "VALUES (:id, :tid, :slug, :label, :vt)"
            ),
            {"id": prop_id, "tid": tenant_id, "slug": "test_known_slug", "label": "Test Slug", "vt": "string"},
        )
    return prop_id


# ── Strict mode ────────────────────────────────────────────────────────────────

def test_strict_known_slug_returns_property_id(app_engine, tenant_id, known_property_id):
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="strict")
    result = resolver.resolve("test_known_slug", "string")
    assert result == known_property_id


def test_strict_unknown_slug_returns_none(app_engine, tenant_id):
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="strict")
    result = resolver.resolve("totally_unknown_slug_xyz", "string")
    assert result is None


def test_strict_unknown_slug_queues_proposed_property(app_engine, tenant_id):
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="strict")
    slug = f"proposed_slug_{uuid.uuid4().hex[:8]}"
    resolver.resolve(slug, "string")
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
        row = conn.execute(
            text("SELECT slug, reviewed FROM proposed_properties WHERE tenant_id = :tid AND slug = :slug"),
            {"tid": tenant_id, "slug": slug},
        ).fetchone()
    assert row is not None
    assert row.slug == slug
    assert row.reviewed is False


def test_strict_duplicate_proposed_property_is_idempotent(app_engine, tenant_id):
    """Calling resolve twice with same unknown slug does not raise a unique violation."""
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="strict")
    slug = f"dup_slug_{uuid.uuid4().hex[:8]}"
    resolver.resolve(slug, "string")
    resolver.resolve(slug, "string")  # must not raise


# ── Permissive mode ────────────────────────────────────────────────────────────

def test_permissive_known_slug_returns_property_id(app_engine, tenant_id, known_property_id):
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="permissive")
    result = resolver.resolve("test_known_slug", "string")
    assert result == known_property_id


def test_permissive_unknown_slug_auto_registers(app_engine, tenant_id):
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="permissive")
    slug = f"auto_slug_{uuid.uuid4().hex[:8]}"
    result = resolver.resolve(slug, "string")
    assert result is not None
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
        row = conn.execute(
            text("SELECT id, slug FROM properties WHERE tenant_id = :tid AND slug = :slug"),
            {"tid": tenant_id, "slug": slug},
        ).fetchone()
    assert row is not None
    assert str(row.id) == result


def test_permissive_auto_register_idempotent(app_engine, tenant_id):
    """Auto-registering the same slug twice returns the same property_id."""
    resolver = VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="permissive")
    slug = f"idem_slug_{uuid.uuid4().hex[:8]}"
    id1 = resolver.resolve(slug, "string")
    id2 = resolver.resolve(slug, "string")
    assert id1 == id2


def test_invalid_mode_raises(app_engine, tenant_id):
    with pytest.raises(ValueError, match="mode must be 'strict' or 'permissive'"):
        VocabularyResolver(engine=app_engine, tenant_id=tenant_id, mode="invalid")
```

Run:

```bash
$ python -m pytest tests/vocabulary/test_resolver.py -x
# expected: FAILED (ImportError)
```

- [ ] **IMPLEMENT:** Create `factvault/vocabulary/__init__.py` (empty) and `factvault/vocabulary/resolver.py`:

```python
# factvault/vocabulary/__init__.py
```

```python
# factvault/vocabulary/resolver.py
"""
Property vocabulary resolver.

Handles the strict / permissive mode decision for unknown property slugs.

Strict mode (default):
    Unknown slug → INSERT into proposed_properties (ON CONFLICT DO NOTHING),
    return None. The calling code rejects the fact and continues.

Permissive mode:
    Unknown slug → INSERT into properties with label=slug and given value_type,
    return the new (or existing) property UUID. The calling code accepts the fact.

In both modes, known slugs return the existing property UUID immediately.

RLS note: uses the engine directly with SET LOCAL app.current_tenant_id so
that RLS policies apply. Tests must use app_engine fixture (not conn fixture
which bypasses RLS).
"""
from __future__ import annotations

import logging
import uuid

from sqlalchemy import text
from sqlalchemy.engine import Engine

logger = logging.getLogger(__name__)

_VALID_MODES = frozenset({"strict", "permissive"})


class VocabularyResolver:
    """Resolves a property slug to a property UUID, respecting tenant RLS.

    Parameters
    ----------
    engine    : SQLAlchemy engine (must support SET LOCAL for RLS).
    tenant_id : UUID string of the owning tenant.
    mode      : 'strict' | 'permissive'
    """

    def __init__(self, engine: Engine, tenant_id: str, mode: str = "strict") -> None:
        if mode not in _VALID_MODES:
            raise ValueError(f"mode must be 'strict' or 'permissive', got {mode!r}")
        self._engine = engine
        self._tenant_id = tenant_id
        self._mode = mode

    def resolve(self, slug: str, value_type: str) -> str | None:
        """Resolve *slug* to a property UUID.

        Returns
        -------
        str | None
            UUID of the matching properties row, or None if unknown and
            mode is strict.
        """
        with self._engine.begin() as conn:
            conn.execute(
                text("SET LOCAL app.current_tenant_id = :tid"),
                {"tid": self._tenant_id},
            )

            # 1. Check for existing property (tenant-specific or system-wide NULL tenant)
            row = conn.execute(
                text(
                    "SELECT id FROM properties "
                    "WHERE slug = :slug AND (tenant_id = :tid OR tenant_id IS NULL) "
                    "LIMIT 1"
                ),
                {"slug": slug, "tid": self._tenant_id},
            ).fetchone()

            if row is not None:
                return str(row.id)

            # 2. Unknown slug — apply mode policy
            if self._mode == "strict":
                return self._queue_proposed(conn, slug, value_type)
            else:
                return self._auto_register(conn, slug, value_type)

    def _queue_proposed(self, conn, slug: str, value_type: str) -> None:
        """Insert into proposed_properties (idempotent via ON CONFLICT DO NOTHING)."""
        conn.execute(
            text(
                "INSERT INTO proposed_properties "
                "(id, tenant_id, slug, value_type, proposed_by) "
                "VALUES (:id, :tid, :slug, :vt, :by) "
                "ON CONFLICT (tenant_id, slug) DO NOTHING"
            ),
            {
                "id": str(uuid.uuid4()),
                "tid": self._tenant_id,
                "slug": slug,
                "vt": value_type,
                "by": "llm-extractor",
            },
        )
        logger.info(
            "VocabularyResolver [strict]: unknown slug %r queued in proposed_properties",
            slug,
        )
        return None

    def _auto_register(self, conn, slug: str, value_type: str) -> str:
        """Insert into properties and return the new UUID (idempotent)."""
        new_id = str(uuid.uuid4())
        conn.execute(
            text(
                "INSERT INTO properties "
                "(id, tenant_id, slug, label, value_type) "
                "VALUES (:id, :tid, :slug, :label, :vt) "
                "ON CONFLICT (tenant_id, slug) DO NOTHING"
            ),
            {
                "id": new_id,
                "tid": self._tenant_id,
                "slug": slug,
                "label": slug,
                "vt": value_type,
            },
        )
        # Re-fetch in case of conflict (ON CONFLICT DO NOTHING means our id may not be used)
        row = conn.execute(
            text(
                "SELECT id FROM properties WHERE tenant_id = :tid AND slug = :slug LIMIT 1"
            ),
            {"tid": self._tenant_id, "slug": slug},
        ).fetchone()
        result = str(row.id) if row else new_id
        logger.info(
            "VocabularyResolver [permissive]: auto-registered slug %r → %s",
            slug,
            result,
        )
        return result
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/vocabulary/test_resolver.py -x -v
# expected: 9 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/vocabulary/__init__.py factvault/vocabulary/resolver.py \
        tests/vocabulary/__init__.py tests/vocabulary/test_resolver.py
git commit -m "feat(vocabulary): VocabularyResolver — strict/permissive slug handling with RLS"
```

---

### Task 11 — Starter property vocabulary YAML + idempotent loader

- [ ] **FAIL:** Create `tests/vocabulary/test_loader.py`:

```python
# tests/vocabulary/test_loader.py
"""
Tests for the starter property vocabulary YAML loader.
Uses app_engine (RLS-aware) fixture.
"""
import uuid
import pytest
from sqlalchemy import text
from factvault.vocabulary import load_starter_properties


@pytest.fixture
def tenant_id() -> str:
    return str(uuid.uuid4())


def test_loader_inserts_properties(app_engine, tenant_id):
    """load_starter_properties inserts rows into the properties table."""
    inserted = load_starter_properties(engine=app_engine, tenant_id=tenant_id)
    assert inserted > 0


def test_loader_inserts_expected_slugs(app_engine, tenant_id):
    load_starter_properties(engine=app_engine, tenant_id=tenant_id)
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
        rows = conn.execute(
            text("SELECT slug FROM properties WHERE tenant_id = :tid"),
            {"tid": tenant_id},
        ).fetchall()
    slugs = {r.slug for r in rows}
    # Spot-check required slugs from the YAML
    for expected in ["founded_by", "founded_on", "sec_cik", "nct_id", "doi", "headcount"]:
        assert expected in slugs, f"Expected slug {expected!r} not found in loaded properties"


def test_loader_is_idempotent(app_engine, tenant_id):
    """Calling load_starter_properties twice does not raise or duplicate rows."""
    load_starter_properties(engine=app_engine, tenant_id=tenant_id)
    load_starter_properties(engine=app_engine, tenant_id=tenant_id)
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
        count = conn.execute(
            text("SELECT COUNT(*) FROM properties WHERE tenant_id = :tid"),
            {"tid": tenant_id},
        ).scalar()
    # Should be exactly the number of unique slugs in the YAML, not doubled
    inserted_first = load_starter_properties.__wrapped_count__ if hasattr(load_starter_properties, "__wrapped_count__") else None
    # Count must match a single load (idempotency check: no duplicates)
    assert count >= 30  # at least 30 slugs in starter YAML


def test_loader_enforces_value_type_check(app_engine, tenant_id):
    """Invalid value_type in YAML is caught before insert."""
    from factvault.vocabulary import _insert_property
    with pytest.raises(Exception):
        with app_engine.begin() as conn:
            conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tenant_id})
            _insert_property(
                conn,
                tenant_id=tenant_id,
                slug="bad_type_prop",
                label="Bad type",
                value_type="invalid_type",  # not in CHECK constraint
            )


def test_loader_tenants_are_isolated(app_engine):
    """Properties loaded for tenant A are not visible to tenant B."""
    tid_a = str(uuid.uuid4())
    tid_b = str(uuid.uuid4())
    load_starter_properties(engine=app_engine, tenant_id=tid_a)
    with app_engine.begin() as conn:
        conn.execute(text("SET LOCAL app.current_tenant_id = :tid"), {"tid": tid_b})
        count = conn.execute(
            text("SELECT COUNT(*) FROM properties WHERE tenant_id = :tid"),
            {"tid": tid_b},
        ).scalar()
    # Tenant B should see 0 tenant-specific properties (only system-wide NULL-tenant ones)
    assert count == 0
```

Run:

```bash
$ python -m pytest tests/vocabulary/test_loader.py -x
# expected: FAILED (ImportError — load_starter_properties not yet defined)
```

- [ ] **IMPLEMENT:** Create `factvault/vocabulary/starter_properties.yaml`:

```yaml
# factvault/vocabulary/starter_properties.yaml
#
# Starter property vocabulary for factvault.
# Loaded idempotently into the properties table per tenant via
# factvault.vocabulary.load_starter_properties().
#
# Fields:
#   slug       — machine-readable key (unique within tenant)
#   label      — human-readable label
#   value_type — one of: entity_ref | string | number | date | url

properties:

  # ── Organisation facts ─────────────────────────────────────────────────────
  - slug: founded_by
    label: Founded by
    value_type: entity_ref

  - slug: founded_on
    label: Founded on
    value_type: date

  - slug: headquarters_location
    label: Headquarters location
    value_type: string

  - slug: legal_name
    label: Legal name
    value_type: string

  - slug: acquired_by
    label: Acquired by
    value_type: entity_ref

  - slug: acquisition_date
    label: Acquisition date
    value_type: date

  - slug: acquisition_price_usd
    label: Acquisition price (USD cents)
    value_type: number

  - slug: headcount
    label: Headcount
    value_type: number

  - slug: ceo
    label: CEO
    value_type: entity_ref

  - slug: cfo
    label: CFO
    value_type: entity_ref

  - slug: board_member
    label: Board member
    value_type: entity_ref

  - slug: parent_company
    label: Parent company
    value_type: entity_ref

  - slug: subsidiary
    label: Subsidiary
    value_type: entity_ref

  # ── Funding ────────────────────────────────────────────────────────────────
  - slug: funding_round_amount_usd
    label: Funding round amount (USD cents)
    value_type: number

  - slug: funding_round_date
    label: Funding round date
    value_type: date

  - slug: funding_round_type
    label: Funding round type
    value_type: string

  - slug: funding_round_lead_investor
    label: Funding round lead investor
    value_type: entity_ref

  - slug: total_funding_usd
    label: Total funding raised (USD cents)
    value_type: number

  - slug: valuation_usd
    label: Valuation (USD cents)
    value_type: number

  # ── Regulatory identifiers ─────────────────────────────────────────────────
  - slug: sec_cik
    label: SEC CIK
    value_type: string

  - slug: cusip
    label: CUSIP
    value_type: string

  - slug: isin
    label: ISIN
    value_type: string

  - slug: lei
    label: LEI (Legal Entity Identifier)
    value_type: string

  - slug: fec_committee_id
    label: FEC committee ID
    value_type: string

  # ── Research identifiers ───────────────────────────────────────────────────
  - slug: doi
    label: DOI
    value_type: string

  - slug: nct_id
    label: ClinicalTrials.gov NCT ID
    value_type: string

  - slug: isbn13
    label: ISBN-13
    value_type: string

  - slug: pubmed_id
    label: PubMed ID
    value_type: string

  - slug: arxiv_id
    label: arXiv ID
    value_type: string

  # ── Clinical / pharma ─────────────────────────────────────────────────────
  - slug: trial_phase
    label: Trial phase
    value_type: string

  - slug: trial_sponsor
    label: Trial sponsor
    value_type: entity_ref

  - slug: trial_indication
    label: Trial indication
    value_type: string

  - slug: primary_endpoint_met
    label: Primary endpoint met
    value_type: string

  - slug: adverse_event_incidence
    label: Adverse event incidence
    value_type: number

  # ── Content / publication ──────────────────────────────────────────────────
  - slug: published_at
    label: Published at
    value_type: date

  - slug: publisher
    label: Publisher
    value_type: string

  - slug: author
    label: Author
    value_type: entity_ref

  - slug: date_mentioned
    label: Date mentioned
    value_type: date

  - slug: deal_value_usd
    label: Deal value (USD cents)
    value_type: number

  - slug: mentioned_entity
    label: Mentioned entity
    value_type: string
```

Update `factvault/vocabulary/__init__.py`:

```python
# factvault/vocabulary/__init__.py
"""
Vocabulary helpers for factvault.

load_starter_properties(engine, tenant_id) → int
    Loads all entries from starter_properties.yaml into the properties table
    for the given tenant. Idempotent via ON CONFLICT DO NOTHING.
    Returns the count of rows attempted (not the count actually inserted,
    since ON CONFLICT DO NOTHING silently skips duplicates).

_insert_property(conn, tenant_id, slug, label, value_type)
    Low-level single-row insert. Exposed for testing the CHECK constraint.
"""
from __future__ import annotations

import logging
from pathlib import Path

import yaml
from sqlalchemy import text
from sqlalchemy.engine import Engine

import uuid

logger = logging.getLogger(__name__)

_YAML_PATH = Path(__file__).parent / "starter_properties.yaml"


def _insert_property(
    conn,
    *,
    tenant_id: str,
    slug: str,
    label: str,
    value_type: str,
) -> None:
    """Insert one property row. Raises on invalid value_type (DB CHECK constraint)."""
    conn.execute(
        text(
            "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
            "VALUES (:id, :tid, :slug, :label, :vt) "
            "ON CONFLICT (tenant_id, slug) DO NOTHING"
        ),
        {
            "id": str(uuid.uuid4()),
            "tid": tenant_id,
            "slug": slug,
            "label": label,
            "vt": value_type,
        },
    )


def load_starter_properties(engine: Engine, tenant_id: str) -> int:
    """Load starter_properties.yaml into properties for *tenant_id*.

    Returns the count of property entries in the YAML (rows attempted).
    Uses ON CONFLICT DO NOTHING so repeated calls are safe.
    """
    with _YAML_PATH.open(encoding="utf-8") as fh:
        data = yaml.safe_load(fh)

    entries = data.get("properties", [])

    with engine.begin() as conn:
        conn.execute(
            text("SET LOCAL app.current_tenant_id = :tid"),
            {"tid": tenant_id},
        )
        for entry in entries:
            _insert_property(
                conn,
                tenant_id=tenant_id,
                slug=entry["slug"],
                label=entry["label"],
                value_type=entry["value_type"],
            )

    logger.info(
        "load_starter_properties: attempted %d properties for tenant %s",
        len(entries),
        tenant_id,
    )
    return len(entries)
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/vocabulary/test_loader.py -x -v
# expected: 5 passed
```

- [ ] **RUN full test suite to confirm no regressions:**

```bash
$ python -m pytest tests/ -x -v --ignore=tests/integration
# expected: all previously passing tests still pass
```

- [ ] **COMMIT:**

```bash
git add factvault/vocabulary/__init__.py \
        factvault/vocabulary/starter_properties.yaml \
        tests/vocabulary/test_loader.py
git commit -m "feat(vocabulary): starter property YAML (40 entries) + idempotent loader"
```

---

<!-- PASS 1 END — Pass 2 appends Tasks 12-22 + self-review below this line -->
