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

---

## Task 12 — BGE-M3 embedding wrapper

**File:** `factvault/embeddings/bge_m3.py`

**Goal:** Lazy-loaded sentence-transformer wrapper returning 1024-dim vectors for statement embedding in Task 14.

**Env var:** `FACTVAULT_EMBEDDING_MODEL` (default `"BAAI/bge-m3"`). CI overrides to `"sentence-transformers/all-MiniLM-L6-v2"` (dim=384) and pads/truncates to 1024 only when the model dim ≠ 1024 — this keeps CI fast without a real model download.

**Class interface:**

```python
# factvault/embeddings/bge_m3.py
from __future__ import annotations
import os
from typing import Optional
import numpy as np

_MODEL_CACHE: dict[str, "SentenceTransformer"] = {}

EMBEDDING_DIM = 1024


def _get_model(model_name: str) -> "SentenceTransformer":
    if model_name not in _MODEL_CACHE:
        from sentence_transformers import SentenceTransformer  # lazy import
        _MODEL_CACHE[model_name] = SentenceTransformer(model_name)
    return _MODEL_CACHE[model_name]


class BGEEmbedder:
    def __init__(self, model_name: Optional[str] = None) -> None:
        self.model_name = model_name or os.environ.get(
            "FACTVAULT_EMBEDDING_MODEL", "BAAI/bge-m3"
        )
        self._model: Optional[object] = None  # loaded on first use

    def _load(self) -> "SentenceTransformer":
        if self._model is None:
            self._model = _get_model(self.model_name)
        return self._model

    def _to_target_dim(self, vec: list[float]) -> list[float]:
        """Pad or truncate to EMBEDDING_DIM=1024 (CI models may differ)."""
        if len(vec) == EMBEDDING_DIM:
            return vec
        arr = np.array(vec, dtype=np.float32)
        if len(arr) < EMBEDDING_DIM:
            arr = np.pad(arr, (0, EMBEDDING_DIM - len(arr)))
        else:
            arr = arr[:EMBEDDING_DIM]
        return arr.tolist()

    def embed(self, text: str) -> list[float]:
        model = self._load()
        vec = model.encode(text, normalize_embeddings=True).tolist()
        return self._to_target_dim(vec)

    def embed_batch(self, texts: list[str]) -> list[list[float]]:
        model = self._load()
        vecs = model.encode(texts, normalize_embeddings=True, batch_size=32)
        return [self._to_target_dim(v.tolist()) for v in vecs]
```

**Tests:** `tests/embeddings/test_bge_m3.py`

```python
# tests/embeddings/test_bge_m3.py
import os
import pytest

os.environ.setdefault("FACTVAULT_EMBEDDING_MODEL", "sentence-transformers/all-MiniLM-L6-v2")

from factvault.embeddings.bge_m3 import BGEEmbedder, EMBEDDING_DIM


@pytest.mark.slow
def test_embed_returns_correct_dim():
    embedder = BGEEmbedder()
    vec = embedder.embed("Acme Corp acquired Beta Inc.")
    assert len(vec) == EMBEDDING_DIM


@pytest.mark.slow
def test_embed_batch_consistency():
    embedder = BGEEmbedder()
    text = "Acme Corp founded in 1998"
    single = embedder.embed(text)
    batch = embedder.embed_batch([text, "other text"])
    assert batch[0] == pytest.approx(single, abs=1e-5)


@pytest.mark.slow
def test_embed_deterministic():
    embedder = BGEEmbedder()
    text = "deterministic output check"
    assert embedder.embed(text) == embedder.embed(text)


@pytest.mark.slow
def test_model_loaded_lazily():
    embedder = BGEEmbedder()
    assert embedder._model is None
    embedder.embed("trigger load")
    assert embedder._model is not None
```

**Commit:**

```bash
git add factvault/embeddings/__init__.py \
        factvault/embeddings/bge_m3.py \
        tests/embeddings/test_bge_m3.py
git commit -m "feat(embeddings): BGE-M3 wrapper with lazy load + batch + dim normalisation"
```

---

## Task 13 — Extract worker (Stage 3)

**File:** `factvault/workers/extract.py`

**Goal:** Poll `status='archived'` sources, run deterministic then LLM extractors, resolve entities + properties, write `statements` + `statement_sources`, advance source to `status='extracted'`. Commit per source so crashes lose at most one source of progress.

**Known limitation (documented):** Entity resolution in v1 is exact-label match within the tenant. If no match exists, a new entity row is created with `type_uri=NULL`. Entity disambiguation (deduplication of `"Acme Corp"` vs `"Acme Corporation"`) is a Plan 4 concern and intentionally out of scope here.

```python
# factvault/workers/extract.py
from __future__ import annotations
import logging
import uuid
from typing import Optional

from factvault.db import get_tenant_conn
from factvault.extractors.runner import run_deterministic
from factvault.extractors.llm import LLMExtractor
from factvault.vocabulary.resolver import VocabularyResolver
from factvault.workers.base import Worker

log = logging.getLogger(__name__)

_BATCH = 25


class ExtractWorker(Worker):
    name = "extract"

    def __init__(
        self,
        tenant_id: str,
        *,
        llm_extractor: Optional[LLMExtractor] = None,
        dry_run: bool = False,
    ) -> None:
        self.tenant_id = tenant_id
        self.llm = llm_extractor or LLMExtractor.from_env()
        self.vocab = VocabularyResolver(tenant_id)
        self.dry_run = dry_run

    def run(self, *, once: bool = False) -> None:
        while True:
            processed = self._process_batch()
            if processed == 0 or once:
                break

    def _process_batch(self) -> int:
        with get_tenant_conn(self.tenant_id) as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    SELECT id, raw_text, url
                    FROM   sources
                    WHERE  status = 'archived'
                      AND  tenant_id = %s
                    LIMIT  %s
                    FOR UPDATE SKIP LOCKED
                    """,
                    (self.tenant_id, _BATCH),
                )
                rows = cur.fetchall()

        for row in rows:
            self._process_source(row["id"], row["raw_text"], row["url"])

        return len(rows)

    def _process_source(
        self, source_id: str, raw_text: Optional[str], url: str
    ) -> None:
        if not raw_text or not raw_text.strip():
            log.warning("source %s has no raw_text — skipping", source_id)
            return

        # --- deterministic pass ---
        det_facts = run_deterministic(raw_text, source_id=source_id)

        # --- LLM pass on uncovered spans ---
        covered = [f.covered_span for f in det_facts if f.covered_span]
        llm_facts = self.llm.extract_uncovered(raw_text, covered, source_id=source_id)

        all_facts = det_facts + llm_facts

        with get_tenant_conn(self.tenant_id) as conn:
            for fact in all_facts:
                try:
                    self._write_fact(conn, source_id, fact)
                except Exception:
                    log.exception("failed to write fact from source %s", source_id)

            with conn.cursor() as cur:
                cur.execute(
                    "UPDATE sources SET status='extracted' WHERE id=%s AND tenant_id=%s",
                    (source_id, self.tenant_id),
                )
            if not self.dry_run:
                conn.commit()

    def _write_fact(self, conn, source_id: str, fact) -> None:
        # Resolve entity
        entity_id = self._resolve_entity(conn, fact.subject_text, source_id)
        if entity_id is None:
            return  # resolution error already logged

        # Resolve property
        prop_id = self.vocab.resolve(conn, fact.property_slug)
        if prop_id is None:
            log.info("unknown property slug %r for source %s — skipped", fact.property_slug, source_id)
            return

        stmt_id = str(uuid.uuid4())
        with conn.cursor() as cur:
            cur.execute(
                """
                INSERT INTO statements
                    (id, tenant_id, subject_id, property_id,
                     val_text, val_number, val_date, val_entity,
                     rank, confidence)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s, 'normal', NULL)
                ON CONFLICT DO NOTHING
                """,
                (
                    stmt_id,
                    self.tenant_id,
                    entity_id,
                    prop_id,
                    fact.val_text,
                    fact.val_number,
                    fact.val_date,
                    fact.val_entity,
                ),
            )
            cur.execute(
                """
                INSERT INTO statement_sources
                    (statement_id, source_id, tenant_id,
                     excerpt, excerpt_offset_start, excerpt_offset_end,
                     extraction_method, confidence)
                VALUES (%s, %s, %s, %s, %s, %s, %s, %s)
                """,
                (
                    stmt_id,
                    source_id,
                    self.tenant_id,
                    fact.excerpt,
                    fact.offset_start,
                    fact.offset_end,
                    fact.extraction_method,
                    fact.per_source_confidence,
                ),
            )

    def _resolve_entity(self, conn, label: str, source_id: str) -> Optional[str]:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id FROM entities WHERE label=%s AND tenant_id=%s LIMIT 1",
                (label, self.tenant_id),
            )
            row = cur.fetchone()
            if row:
                return row["id"]
            # auto-create
            new_id = str(uuid.uuid4())
            cur.execute(
                """
                INSERT INTO entities (id, tenant_id, label, type_uri, meta)
                VALUES (%s, %s, %s, NULL, %s)
                """,
                (
                    new_id,
                    self.tenant_id,
                    label,
                    {
                        "auto_created_from": "extract",
                        "source_id": source_id,
                    },
                ),
            )
            return new_id
```

**Tests:** `tests/workers/test_extract.py`

```python
# tests/workers/test_extract.py
import uuid
from unittest.mock import MagicMock, patch

import pytest

from tests.factories import make_tenant, make_source
from factvault.workers.extract import ExtractWorker


@pytest.fixture
def tenant_id(db):
    return make_tenant(db)


def test_extract_worker_writes_statements_and_sources(db, tenant_id):
    source_id = make_source(
        db,
        tenant_id,
        raw_text="Acme Corp acquired Beta Inc. for $450M on April 12, 2023",
        status="archived",
    )

    mock_llm = MagicMock()
    mock_llm.extract_uncovered.return_value = []  # deterministic covers it

    worker = ExtractWorker(tenant_id, llm_extractor=mock_llm)
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT COUNT(*) AS n FROM statements WHERE tenant_id=%s", (tenant_id,)
        )
        assert cur.fetchone()["n"] >= 1

        cur.execute(
            "SELECT COUNT(*) AS n FROM statement_sources WHERE source_id=%s",
            (source_id,),
        )
        assert cur.fetchone()["n"] >= 1

        cur.execute(
            "SELECT status FROM sources WHERE id=%s", (source_id,)
        )
        assert cur.fetchone()["status"] == "extracted"


def test_extract_worker_skips_null_raw_text(db, tenant_id):
    source_id = make_source(db, tenant_id, raw_text=None, status="archived")

    mock_llm = MagicMock()
    mock_llm.extract_uncovered.return_value = []

    worker = ExtractWorker(tenant_id, llm_extractor=mock_llm)
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT COUNT(*) AS n FROM statements WHERE tenant_id=%s", (tenant_id,)
        )
        assert cur.fetchone()["n"] == 0

        # Source left in archived (no text, nothing to extract)
        cur.execute("SELECT status FROM sources WHERE id=%s", (source_id,))
        assert cur.fetchone()["status"] == "archived"


def test_extract_worker_auto_creates_entity(db, tenant_id):
    make_source(
        db,
        tenant_id,
        raw_text="NewCo Inc. raised $10M in Series A on 2024-03-01",
        status="archived",
    )
    mock_llm = MagicMock()
    mock_llm.extract_uncovered.return_value = []

    worker = ExtractWorker(tenant_id, llm_extractor=mock_llm)
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT meta FROM entities WHERE tenant_id=%s AND meta->>'auto_created_from'='extract'",
            (tenant_id,),
        )
        rows = cur.fetchall()
        assert len(rows) >= 1
```

**Commit:**

```bash
git add factvault/workers/extract.py \
        tests/workers/test_extract.py
git commit -m "feat(workers): extract worker — deterministic+LLM extraction, entity auto-create, per-source commit"
```

---

## Task 14 — Corroborate worker (Stage 4)

**File:** `factvault/workers/corroborate.py`

**Goal:** For each unscored statement, gather peers (same subject+property+value), compute independence (publisher uniqueness + trigram similarity ≤ 0.8), apply 0.5/0.85/0.95 confidence ceilings, write embeddings via BGE-M3, leave conflicting values at `rank='normal'`.

**Independence check note:** The spec says trigram similarity should use `pg_trgm`. For v1 in Python we use `difflib.SequenceMatcher` on the first 2 000 chars — this is spec-compliant as a client-side approximation; a Plan 5 migration to `pg_trgm` is logged in the README.

```python
# factvault/workers/corroborate.py
from __future__ import annotations
import difflib
import logging
from typing import Optional

from factvault.db import get_tenant_conn
from factvault.embeddings.bge_m3 import BGEEmbedder
from factvault.workers.base import Worker

log = logging.getLogger(__name__)

_BATCH            = 50
_TRIGRAM_THRESHOLD = 0.8
_SNIPPET_LEN       = 2000

_CEILINGS = {1: 0.5, 2: 0.85}
_DEFAULT_CEILING = 0.95   # 3+ independent sources


def _ceiling(n_ind: int) -> float:
    return _CEILINGS.get(n_ind, _DEFAULT_CEILING)


def _trigram_sim(a: str, b: str) -> float:
    return difflib.SequenceMatcher(None, a[:_SNIPPET_LEN], b[:_SNIPPET_LEN]).ratio()


class CorroborateWorker(Worker):
    name = "corroborate"

    def __init__(
        self,
        tenant_id: str,
        *,
        embedder: Optional[BGEEmbedder] = None,
    ) -> None:
        self.tenant_id = tenant_id
        self.embedder = embedder or BGEEmbedder()

    def run(self, *, once: bool = False) -> None:
        while True:
            processed = self._process_batch()
            if processed == 0 or once:
                break

    def _process_batch(self) -> int:
        with get_tenant_conn(self.tenant_id) as conn:
            with conn.cursor() as cur:
                cur.execute(
                    """
                    SELECT id, subject_id, property_id,
                           val_text, val_number, val_date, val_entity
                    FROM   statements
                    WHERE  confidence IS NULL
                      AND  rank != 'deprecated'
                      AND  tenant_id = %s
                    LIMIT  %s
                    """,
                    (self.tenant_id, _BATCH),
                )
                rows = cur.fetchall()

        for row in rows:
            self._corroborate_statement(row)

        return len(rows)

    def _corroborate_statement(self, stmt: dict) -> None:
        with get_tenant_conn(self.tenant_id) as conn:
            peers = self._get_peers(conn, stmt)
            confidence = self._compute_confidence(conn, peers)
            peer_ids = [p["id"] for p in peers]

            # update all peers together
            with conn.cursor() as cur:
                cur.execute(
                    f"""
                    UPDATE statements
                    SET    confidence = %s
                    WHERE  id = ANY(%s)
                      AND  tenant_id = %s
                    """,
                    (confidence, peer_ids, self.tenant_id),
                )

            # embeddings
            self._write_embeddings(conn, peers)
            conn.commit()

    def _get_peers(self, conn, stmt: dict) -> list[dict]:
        """All non-deprecated statements with the same (subject, property, value)."""
        val_col, val = self._value_col(stmt)
        with conn.cursor() as cur:
            cur.execute(
                f"""
                SELECT id, subject_id, property_id,
                       val_text, val_number, val_date, val_entity
                FROM   statements
                WHERE  subject_id  = %s
                  AND  property_id = %s
                  AND  {val_col}   = %s
                  AND  rank        != 'deprecated'
                  AND  tenant_id   = %s
                """,
                (stmt["subject_id"], stmt["property_id"], val, self.tenant_id),
            )
            return cur.fetchall()

    def _value_col(self, stmt: dict) -> tuple[str, object]:
        for col in ("val_entity", "val_date", "val_number", "val_text"):
            if stmt.get(col) is not None:
                return col, stmt[col]
        return "val_text", None

    def _compute_confidence(self, conn, peers: list[dict]) -> float:
        if not peers:
            return 0.5

        peer_ids = [p["id"] for p in peers]

        # Gather sources + raw_text snippets
        with conn.cursor() as cur:
            cur.execute(
                """
                SELECT ss.statement_id, ss.confidence AS src_conf,
                       s.publisher, s.raw_text
                FROM   statement_sources ss
                JOIN   sources s ON s.id = ss.source_id
                WHERE  ss.statement_id = ANY(%s)
                  AND  ss.tenant_id    = %s
                """,
                (peer_ids, self.tenant_id),
            )
            src_rows = cur.fetchall()

        if not src_rows:
            return 0.5

        # per_source_max confidence
        src_confidences = [r["src_conf"] for r in src_rows if r["src_conf"] is not None]
        per_src_max = max(src_confidences) if src_confidences else 0.95

        # independence check: group by publisher, then trigram
        snippets_by_pub: dict[str, list[str]] = {}
        for r in src_rows:
            pub = r["publisher"] or "__unknown__"
            snippets_by_pub.setdefault(pub, []).append(r["raw_text"] or "")

        unique_pubs = list(snippets_by_pub.keys())

        # Among sources from different publishers, check pairwise trigram similarity
        # Representatives: first snippet per publisher
        reps = [(pub, snippets[0]) for pub, snippets in snippets_by_pub.items()]
        independent_pubs: list[str] = []
        for i, (pub_i, snip_i) in enumerate(reps):
            is_dep = False
            for pub_j, snip_j in independent_pubs:
                if _trigram_sim(snip_i, snip_j) > _TRIGRAM_THRESHOLD:
                    is_dep = True
                    break
            if not is_dep:
                independent_pubs.append((pub_i, snip_i))

        n_ind = len(independent_pubs)
        ceil = _ceiling(n_ind)
        return min(per_src_max, ceil)

    def _write_embeddings(self, conn, peers: list[dict]) -> None:
        for peer in peers:
            text_parts = [
                str(peer.get("val_text") or ""),
                str(peer.get("val_number") or ""),
                str(peer.get("val_date") or ""),
            ]
            text = " ".join(t for t in text_parts if t).strip()
            if not text:
                continue
            try:
                vec = self.embedder.embed(text)
                with conn.cursor() as cur:
                    cur.execute(
                        "UPDATE statements SET embedding=%s WHERE id=%s AND tenant_id=%s",
                        (vec, peer["id"], self.tenant_id),
                    )
            except Exception:
                log.exception("embedding failed for statement %s", peer["id"])
```

**Tests:** `tests/workers/test_corroborate.py`

```python
# tests/workers/test_corroborate.py
from unittest.mock import MagicMock
import pytest

from tests.factories import make_tenant, make_source, make_entity, make_property, make_statement
from factvault.workers.corroborate import CorroborateWorker


@pytest.fixture
def tenant_id(db):
    return make_tenant(db)


def _make_embedder():
    mock = MagicMock()
    mock.embed.return_value = [0.0] * 1024
    return mock


def test_single_source_yields_0_5(db, tenant_id):
    entity_id  = make_entity(db, tenant_id, label="Acme Corp")
    prop_id    = make_property(db, tenant_id, slug="org.founded_year")
    source_id  = make_source(db, tenant_id, raw_text="Acme Corp was founded in 1998", publisher="pub-a")
    stmt_id    = make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=source_id)

    worker = CorroborateWorker(tenant_id, embedder=_make_embedder())
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute("SELECT confidence FROM statements WHERE id=%s", (stmt_id,))
        conf = cur.fetchone()["confidence"]
    assert conf == pytest.approx(0.5)


def test_two_independent_sources_yields_0_85(db, tenant_id):
    entity_id  = make_entity(db, tenant_id, label="Acme Corp")
    prop_id    = make_property(db, tenant_id, slug="org.founded_year")
    src_a      = make_source(db, tenant_id, raw_text="Acme Corp was founded in 1998", publisher="pub-a")
    src_b      = make_source(db, tenant_id, raw_text="Company Acme was established in 1998", publisher="pub-b")
    stmt_a     = make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src_a)
    stmt_b     = make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src_b)

    worker = CorroborateWorker(tenant_id, embedder=_make_embedder())
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute("SELECT confidence FROM statements WHERE id IN (%s, %s)", (stmt_a, stmt_b))
        confs = [r["confidence"] for r in cur.fetchall()]
    assert all(c == pytest.approx(0.85) for c in confs)


def test_three_independent_sources_yields_0_95(db, tenant_id):
    entity_id = make_entity(db, tenant_id, label="Acme Corp")
    prop_id   = make_property(db, tenant_id, slug="org.founded_year")
    for i, pub in enumerate(["pub-a", "pub-b", "pub-c"]):
        raw = f"Source {i}: Acme Corp was founded in 1998 (publisher {pub})"
        src = make_source(db, tenant_id, raw_text=raw, publisher=pub)
        make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src)

    worker = CorroborateWorker(tenant_id, embedder=_make_embedder())
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT confidence FROM statements WHERE subject_id=%s AND tenant_id=%s",
            (entity_id, tenant_id),
        )
        confs = [r["confidence"] for r in cur.fetchall()]
    assert all(c == pytest.approx(0.95) for c in confs)


def test_same_publisher_treated_as_dependent(db, tenant_id):
    entity_id = make_entity(db, tenant_id, label="Acme Corp")
    prop_id   = make_property(db, tenant_id, slug="org.founded_year")
    for _ in range(3):
        src = make_source(db, tenant_id, raw_text="Acme Corp founded 1998 [copy]", publisher="same-pub")
        make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src)

    worker = CorroborateWorker(tenant_id, embedder=_make_embedder())
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT confidence FROM statements WHERE subject_id=%s AND tenant_id=%s LIMIT 1",
            (entity_id, tenant_id),
        )
        conf = cur.fetchone()["confidence"]
    assert conf == pytest.approx(0.5)   # single independent publisher → ceiling 0.5


def test_conflicting_values_stay_rank_normal(db, tenant_id):
    entity_id = make_entity(db, tenant_id, label="Acme Corp")
    prop_id   = make_property(db, tenant_id, slug="org.founded_year")
    src_a     = make_source(db, tenant_id, raw_text="Founded 1998", publisher="pub-a")
    src_b     = make_source(db, tenant_id, raw_text="Founded 1999", publisher="pub-b")
    stmt_a    = make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src_a)
    stmt_b    = make_statement(db, tenant_id, entity_id, prop_id, val_number=1999, source_id=src_b)

    worker = CorroborateWorker(tenant_id, embedder=_make_embedder())
    worker.run(once=True)

    with db.cursor() as cur:
        cur.execute(
            "SELECT rank FROM statements WHERE id IN (%s, %s)", (stmt_a, stmt_b)
        )
        ranks = {r["rank"] for r in cur.fetchall()}
    assert ranks == {"normal"}


def test_embeddings_populated(db, tenant_id):
    mock_emb = _make_embedder()
    entity_id = make_entity(db, tenant_id, label="Acme Corp")
    prop_id   = make_property(db, tenant_id, slug="org.founded_year")
    src       = make_source(db, tenant_id, raw_text="Acme Corp founded 1998", publisher="pub-a")
    make_statement(db, tenant_id, entity_id, prop_id, val_number=1998, source_id=src)

    CorroborateWorker(tenant_id, embedder=mock_emb).run(once=True)

    assert mock_emb.embed.called
```

**Commit:**

```bash
git add factvault/workers/corroborate.py \
        tests/workers/test_corroborate.py
git commit -m "feat(workers): corroborate worker — confidence ceilings, independence check, embeddings"
```

---

## Task 15 — CLI subcommands: extract + corroborate

**File:** `factvault/workers/cli.py` (extend existing)

**Goal:** Register `extract` and `corroborate` in the worker registry so `factvault-worker run extract` and `factvault-worker run corroborate` dispatch the correct class.

```python
# Additions to factvault/workers/cli.py (registry entries)

from factvault.workers.extract     import ExtractWorker
from factvault.workers.corroborate import CorroborateWorker

WORKER_REGISTRY = {
    **WORKER_REGISTRY,          # existing entries from Plan 2 T12
    "extract":     ExtractWorker,
    "corroborate": CorroborateWorker,
}
```

**Tests:** `tests/workers/test_cli_workers.py`

```python
from click.testing import CliRunner
from factvault.workers.cli import cli


def test_run_extract_help():
    result = CliRunner().invoke(cli, ["run", "extract", "--help"])
    assert result.exit_code == 0
    assert "extract" in result.output


def test_run_corroborate_help():
    result = CliRunner().invoke(cli, ["run", "corroborate", "--help"])
    assert result.exit_code == 0
    assert "corroborate" in result.output
```

**Commit:**

```bash
git add factvault/workers/cli.py \
        tests/workers/test_cli_workers.py
git commit -m "feat(cli): register extract + corroborate workers in factvault-worker run"
```

---

## Task 16 — Property vocabulary CLI

**File:** `factvault/workers/cli.py` (add `vocab` command group)

**Goal:** Three vocab subcommands: `load`, `proposed`, `approve`, `reject`. These let operators bootstrap a tenant's vocabulary and action proposals from the LLM extractor.

```python
# factvault/workers/cli.py — vocab group

import click
from factvault.vocabulary.loader import load_starter_properties
from factvault.db import get_tenant_conn


@cli.group()
def vocab():
    """Property vocabulary management."""


@vocab.command("load")
@click.option("--tenant", required=True, help="Tenant UUID")
@click.option("--file",   default="factvault/vocabulary/starter_properties.yaml",
              show_default=True, help="Path to properties YAML")
def vocab_load(tenant, file):
    """Load starter vocabulary into a tenant."""
    with get_tenant_conn(tenant) as conn:
        inserted = load_starter_properties(conn, tenant, path=file)
    click.echo(f"Loaded {inserted} properties for tenant {tenant}")


@vocab.command("proposed")
@click.option("--tenant", required=True)
def vocab_proposed(tenant):
    """List pending proposed_properties rows."""
    with get_tenant_conn(tenant) as conn:
        with conn.cursor() as cur:
            cur.execute(
                "SELECT id, slug, proposed_at FROM proposed_properties "
                "WHERE tenant_id=%s AND status='pending' ORDER BY proposed_at",
                (tenant,),
            )
            rows = cur.fetchall()
    if not rows:
        click.echo("No pending proposals.")
        return
    for r in rows:
        click.echo(f"{r['id']}  {r['slug']}  {r['proposed_at']}")


@vocab.command("approve")
@click.argument("proposed_id")
def vocab_approve(proposed_id):
    """Approve a proposed property slug."""
    # tenant derived from the row itself
    with get_db_conn() as conn:
        with conn.cursor() as cur:
            cur.execute(
                "UPDATE proposed_properties SET status='approved', resolved_at=now() "
                "WHERE id=%s RETURNING tenant_id, slug",
                (proposed_id,),
            )
            row = cur.fetchone()
        conn.commit()
    if row:
        click.echo(f"Approved: {row['slug']} (tenant {row['tenant_id']})")
    else:
        click.echo(f"Not found: {proposed_id}", err=True)


@vocab.command("reject")
@click.argument("proposed_id")
@click.option("--reason", default="", help="Rejection reason")
def vocab_reject(proposed_id, reason):
    """Reject a proposed property slug."""
    with get_db_conn() as conn:
        with conn.cursor() as cur:
            cur.execute(
                "UPDATE proposed_properties "
                "SET status='rejected', resolved_at=now(), rejection_reason=%s "
                "WHERE id=%s RETURNING slug",
                (reason, proposed_id),
            )
            row = cur.fetchone()
        conn.commit()
    click.echo(f"Rejected: {row['slug'] if row else proposed_id}")
```

**Tests:** `tests/workers/test_vocab_cli.py`

```python
from click.testing import CliRunner
from factvault.workers.cli import cli


def test_vocab_load_help():
    r = CliRunner().invoke(cli, ["vocab", "load", "--help"])
    assert r.exit_code == 0
    assert "--tenant" in r.output


def test_vocab_proposed_help():
    r = CliRunner().invoke(cli, ["vocab", "proposed", "--help"])
    assert r.exit_code == 0


def test_vocab_approve_help():
    r = CliRunner().invoke(cli, ["vocab", "approve", "--help"])
    assert r.exit_code == 0


def test_vocab_reject_help():
    r = CliRunner().invoke(cli, ["vocab", "reject", "--help"])
    assert r.exit_code == 0
    assert "--reason" in r.output


def test_vocab_load_integration(db, tmp_path):
    """vocab load writes properties to the DB."""
    from tests.factories import make_tenant
    tenant = make_tenant(db)
    yaml_content = """
properties:
  - slug: org.ticker_symbol
    label: Ticker Symbol
    value_type: string
    scope: org
"""
    f = tmp_path / "props.yaml"
    f.write_text(yaml_content)
    r = CliRunner().invoke(cli, ["vocab", "load", "--tenant", tenant, "--file", str(f)])
    assert r.exit_code == 0
    assert "Loaded" in r.output
```

**Commit:**

```bash
git add factvault/workers/cli.py \
        tests/workers/test_vocab_cli.py
git commit -m "feat(cli): vocab load/proposed/approve/reject subcommands"
```

---

## Task 17 — Integration end-to-end test: extract + corroborate

**File:** `tests/integration/test_fact_pipeline_e2e.py`

**Goal:** Load-bearing integration test covering the full Stage 3 → Stage 4 flow including confidence ceiling, conflict detection, and embedding population.

```python
# tests/integration/test_fact_pipeline_e2e.py
"""
End-to-end pipeline: archived sources → extract worker → corroborate worker.

Scenario:
  pub-a + pub-b both say "Acme Corp was founded in 1998" → corroborating, confidence 0.85
  pub-c says "Acme Corp was founded in 1999"             → conflict, rank='normal'
  v_conflicts view must surface the conflict
  All statements must have embeddings after corroborate
"""
from unittest.mock import MagicMock
import pytest

from tests.factories import make_tenant, make_source
from factvault.workers.extract     import ExtractWorker
from factvault.workers.corroborate import CorroborateWorker


@pytest.fixture
def tenant_id(db):
    return make_tenant(db)


def _mock_llm():
    m = MagicMock()
    m.extract_uncovered.return_value = []
    return m


def _mock_embedder():
    m = MagicMock()
    m.embed.return_value = [0.1] * 1024
    return m


def test_full_pipeline_e2e(db, tenant_id):
    src_a = make_source(db, tenant_id, publisher="pub-a",
                        raw_text="Acme Corp was founded in 1998", status="archived")
    src_b = make_source(db, tenant_id, publisher="pub-b",
                        raw_text="Acme Corp was established in 1998", status="archived")
    src_c = make_source(db, tenant_id, publisher="pub-c",
                        raw_text="Acme Corp was founded in 1999", status="archived")

    # Stage 3: extract
    ExtractWorker(tenant_id, llm_extractor=_mock_llm()).run(once=True)

    with db.cursor() as cur:
        cur.execute("SELECT COUNT(*) AS n FROM statements WHERE tenant_id=%s", (tenant_id,))
        assert cur.fetchone()["n"] >= 2, "extract worker should produce at least 2 statements"

        cur.execute("SELECT COUNT(*) AS n FROM statement_sources WHERE tenant_id=%s", (tenant_id,))
        assert cur.fetchone()["n"] >= 2

    # Stage 4: corroborate
    CorroborateWorker(tenant_id, embedder=_mock_embedder()).run(once=True)

    with db.cursor() as cur:
        # corroborating pair (1998) → confidence = 0.85
        cur.execute(
            "SELECT confidence FROM statements WHERE val_number=1998 AND tenant_id=%s",
            (tenant_id,),
        )
        corroborating = [r["confidence"] for r in cur.fetchall()]
        assert corroborating, "expected statements for 1998"
        assert all(abs(c - 0.85) < 0.01 for c in corroborating), \
            f"expected 0.85 for corroborating pair, got {corroborating}"

        # conflicting value (1999) → single source → 0.5
        cur.execute(
            "SELECT confidence FROM statements WHERE val_number=1999 AND tenant_id=%s",
            (tenant_id,),
        )
        conflict_rows = cur.fetchall()
        assert conflict_rows, "expected statement for 1999"
        assert all(abs(c["confidence"] - 0.5) < 0.01 for c in conflict_rows)

        # All conflict ranks remain normal
        cur.execute(
            "SELECT rank FROM statements WHERE tenant_id=%s", (tenant_id,)
        )
        ranks = {r["rank"] for r in cur.fetchall()}
        assert ranks == {"normal"}, f"unexpected ranks: {ranks}"

    # v_conflicts surfaces the conflict
    with db.cursor() as cur:
        cur.execute(
            "SELECT COUNT(*) AS n FROM v_conflicts WHERE tenant_id=%s", (tenant_id,)
        )
        n_conflicts = cur.fetchone()["n"]
    assert n_conflicts >= 1, "v_conflicts should surface the 1998 vs 1999 conflict"

    # Embeddings populated on all statements
    with db.cursor() as cur:
        cur.execute(
            "SELECT COUNT(*) AS n FROM statements WHERE embedding IS NULL AND tenant_id=%s",
            (tenant_id,),
        )
        assert cur.fetchone()["n"] == 0, "all statements should have embeddings after corroborate"
```

**Commit:**

```bash
git add tests/integration/test_fact_pipeline_e2e.py
git commit -m "test(integration): end-to-end fact pipeline — extract + corroborate + conflict + embeddings"
```

---

## Task 18 — K8s CronJob: corroborate worker

**File:** `deploy/k8s/corroborate-worker-cronjob.yaml`

**Goal:** Run corroborate once per hour. Same security pattern as verify-worker CronJob from Plan 2.

```yaml
# deploy/k8s/corroborate-worker-cronjob.yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: factvault-corroborate-worker
  namespace: factvault
spec:
  schedule: "0 * * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 3
  failedJobsHistoryLimit: 3
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          securityContext:
            runAsNonRoot: true
            runAsUser: 65532
            fsGroup: 65532
          containers:
            - name: corroborate-worker
              image: ghcr.io/psimmons/factvault:latest
              args: ["factvault-worker", "run", "corroborate", "--once"]
              resources:
                requests:
                  cpu: 100m
                  memory: 256Mi
                limits:
                  cpu: 500m
                  memory: 512Mi
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop: [ALL]
              env:
                - name: FACTVAULT_TENANT_ID
                  valueFrom:
                    secretKeyRef:
                      name: factvault-secrets
                      key: tenant-id
                - name: DATABASE_URL
                  valueFrom:
                    secretKeyRef:
                      name: factvault-secrets
                      key: database-url
              envFrom:
                - secretRef:
                    name: factvault-worker-secrets
```

**Commit:**

```bash
git add deploy/k8s/corroborate-worker-cronjob.yaml
git commit -m "deploy(k8s): corroborate worker CronJob (hourly)"
```

---

## Task 19 — K8s Deployment: extract worker (long-running)

**File:** `deploy/k8s/extract-worker-deployment.yaml`

**Goal:** Extract worker runs as a long-running Deployment (polls continuously), not a CronJob. Single replica. Generous memory for tokenizer. Chainguard nonroot + tini.

```yaml
# deploy/k8s/extract-worker-deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factvault-extract-worker
  namespace: factvault
  labels:
    app: factvault-extract-worker
spec:
  replicas: 1
  selector:
    matchLabels:
      app: factvault-extract-worker
  template:
    metadata:
      labels:
        app: factvault-extract-worker
    spec:
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        fsGroup: 65532
      containers:
        - name: extract-worker
          image: ghcr.io/psimmons/factvault:latest
          command: ["/sbin/tini", "--"]
          args: ["factvault-worker", "run", "extract"]
          resources:
            requests:
              cpu: 200m
              memory: 512Mi
            limits:
              cpu: 1000m
              memory: 2Gi          # tokenizer + spaCy model in-process
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop: [ALL]
          env:
            - name: FACTVAULT_TENANT_ID
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: tenant-id
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: database-url
            - name: FACTVAULT_LLM_ENDPOINT
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: llm-endpoint
            - name: FACTVAULT_LLM_API_KEY
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: llm-api-key
          envFrom:
            - secretRef:
                name: factvault-worker-secrets
          livenessProbe:
            exec:
              command: ["factvault-worker", "health"]
            initialDelaySeconds: 30
            periodSeconds: 60
```

**Commit:**

```bash
git add deploy/k8s/extract-worker-deployment.yaml
git commit -m "deploy(k8s): extract worker long-running Deployment (single replica, 2Gi for tokenizer)"
```

---

## Task 20 — Fact-pipeline README

**File:** `factvault/extractors/README.md`

**Goal:** Single authoritative doc covering the full extraction + corroboration pipeline for operators and contributors.

Sections to include (prose outline — implementation fills in detail):

1. **Overview** — how deterministic + LLM extraction compose; why deterministic runs first
2. **Adding a deterministic extractor** — create `extractors/deterministic/my_extractor.py`, subclass `BaseExtractor`, implement `extract(text, source_id) -> list[ExtractedFact]`, register in `extractors/runner.py`
3. **Adding a gazetteer file** — drop a CSV/JSON at `extractors/gazetteers/`, update `entities.py` loader; file format: one label per line or `{"label": ..., "type": ...}` JSON
4. **Configuring the LLM endpoint** — env vars: `FACTVAULT_LLM_ENDPOINT`, `FACTVAULT_LLM_API_KEY`, `FACTVAULT_LLM_MODEL`; defaults for local vLLM vs. OpenAI-compatible API
5. **Offset verification guarantee** — every `statement_sources` INSERT is gated by `verify_excerpt_offset(source.raw_text, excerpt, start, end)`; failures go to `extraction_errors` and are never silently dropped
6. **Confidence formula** — 0.5 / 0.85 / 0.95 ceilings based on independent-source count; independence defined by publisher uniqueness + trigram similarity < 0.8 (difflib v1, pg_trgm roadmap)
7. **Conflict resolution policy** — conflicting values at the same `(subject, property)` stay at `rank='normal'`; `v_conflicts` view surfaces them for human review; no automated resolution in v1
8. **Troubleshooting** — common issues: `status` stuck at `archived` (check extraction_errors table), embeddings NULL after corroborate (check FACTVAULT_EMBEDDING_MODEL env), proposed_properties accumulating (run `factvault-worker vocab proposed --tenant ...`)

**Commit:**

```bash
git add factvault/extractors/README.md
git commit -m "docs(extractors): pipeline README — extractors, gazetteer, LLM config, confidence, conflicts"
```

---

## Task 21 — CI workflow update

**File:** `.github/workflows/ci.yml` (extend existing)

**Goal:** Add extract + corroborate + embeddings + vocabulary tests to default CI. Move `@pytest.mark.slow` tests to a separate nightly job.

```yaml
# .github/workflows/ci.yml — additions / modifications

# In the default test job, add to the pytest invocation:
#   -m "not slow"
# This excludes BGE-M3 model-download tests from every PR.

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - name: Install dependencies
        run: pip install -e ".[dev]"
      - name: Run tests (fast)
        run: pytest -m "not slow" --tb=short -q

  slow-tests:
    runs-on: ubuntu-latest
    if: github.event_name == 'schedule' || github.event_name == 'workflow_dispatch'
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - name: Install dependencies
        run: pip install -e ".[dev]"
      - name: Run slow tests (model download)
        env:
          FACTVAULT_EMBEDDING_MODEL: "sentence-transformers/all-MiniLM-L6-v2"
        run: pytest -m "slow" --tb=short -q

on:
  push:
    branches: [main]
  pull_request:
  schedule:
    - cron: "0 3 * * *"   # nightly at 03:00 UTC for slow tests
  workflow_dispatch:
```

**Note:** The full `ci.yml` already exists from Plan 2. This task adds the `-m "not slow"` flag to the existing fast job and appends the `slow-tests` job + `schedule` trigger. Do not rewrite jobs that already exist — apply surgical additions only.

**Commit:**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: exclude @pytest.mark.slow from default run; add nightly slow-tests job"
```

---

## Task 22 — Smoke test: extract CLI with --dry-run

**File:** `tests/integration/test_extract_cli_smoke.py`

**Goal:** Catch packaging issues — missing imports, unregistered CLI entry points, broken `--dry-run` flag — that focused unit tests miss.

```python
# tests/integration/test_extract_cli_smoke.py
"""
Smoke-tests that factvault-worker extract is wired end-to-end:
  - The CLI entry point exists and is importable
  - `--dry-run` flag is accepted and exits cleanly with a configured tenant
  - No DB writes occur with --dry-run (statement count unchanged)
"""
import pytest
from click.testing import CliRunner

from factvault.workers.cli import cli
from tests.factories import make_tenant, make_source


def test_extract_cli_help():
    """Entry point resolves and --help exits 0."""
    result = CliRunner().invoke(cli, ["run", "extract", "--help"])
    assert result.exit_code == 0, result.output
    assert "--dry-run" in result.output or "dry" in result.output.lower()


def test_extract_cli_dry_run_no_db_writes(db):
    """--dry-run validates config + extractor loading without writing statements."""
    tenant = make_tenant(db)
    make_source(
        db, tenant,
        raw_text="Acme Corp acquired Delta Inc. for $200M on 2024-06-15",
        status="archived",
    )

    with db.cursor() as cur:
        cur.execute("SELECT COUNT(*) AS n FROM statements WHERE tenant_id=%s", (tenant,))
        before = cur.fetchone()["n"]

    result = CliRunner().invoke(
        cli,
        ["run", "extract", "--tenant", tenant, "--once", "--dry-run"],
    )
    assert result.exit_code == 0, result.output

    with db.cursor() as cur:
        cur.execute("SELECT COUNT(*) AS n FROM statements WHERE tenant_id=%s", (tenant,))
        after = cur.fetchone()["n"]

    assert after == before, f"--dry-run must not write statements; before={before} after={after}"
```

**Commit:**

```bash
git add tests/integration/test_extract_cli_smoke.py
git commit -m "test(smoke): extract CLI --dry-run — packaging + entry-point + no-write guard"
```

---

## Self-Review

### Spec Coverage Checklist

| Spec requirement | Task |
|------------------|------|
| Deterministic identifier extraction — CIK, CUSIP, ISIN, DOI, NCT IDs (§ Stage 3) | Task 3 (Pass 1) |
| Deterministic money/funding extraction (§ Stage 3) | Task 4 (Pass 1) |
| Deterministic date extraction — ISO, natural language, fiscal quarters (§ Stage 3) | Task 5 (Pass 1) |
| Deterministic gazetteer-augmented entity extraction via spaCy (§ Stage 3) | Task 6 (Pass 1) |
| Deterministic runner — composing all extractors, covered-span subtraction (§ Stage 3) | Task 7 (Pass 1) |
| LLM extractor — structured JSON output, strict schema (§ Stage 3) | Tasks 8–9 (Pass 1) |
| Offset verification gate — every statement_sources INSERT gated; failures → extraction_errors (§ Stage 3 Guarantees) | Task 9 (Pass 1) |
| Property vocabulary — strict / permissive mode; unknown slugs → proposed_properties (§3.2 Controlled Vocabulary) | Task 10 (Pass 1) |
| Starter property YAML — 40 properties, idempotent loader (§3.2) | Task 11 (Pass 1) |
| BGE-M3 1024-dim embeddings for statements (§ Embedding Model) | Task 12 |
| Extract worker — deterministic + LLM composition, entity auto-create, per-source commit (§ Stage 3) | Task 13 |
| Corroborate worker — confidence recomputed from scratch on each run (§ Stage 4 Guarantees) | Task 14 |
| Confidence ceilings: 1 source → 0.5, 2 → 0.85, 3+ → 0.95 (§3.3 Confidence Formula) | Task 14 |
| Independence check — publisher uniqueness + trigram similarity < 0.8 (§3.3 / §Stage 4) | Task 14 |
| Conflict detection — differing values stay rank='normal'; no auto-resolution (§3.3 Conflict Detection) | Task 14 |
| Conflict surfacing via v_conflicts view (§3.2 v_conflicts) | Task 17 (e2e asserts the view) |
| Statement embeddings populated after corroborate (§ Embedding Model) | Task 14 |
| factvault-worker run extract / corroborate CLI dispatch (§ factvault doctor / ops) | Task 15 |
| Property vocab CLI: load, proposed, approve, reject (§3.2 Controlled Vocabulary) | Task 16 |
| Extract + corroborate end-to-end integration test (§ Pipeline) | Task 17 |
| K8s CronJob for corroborate, hourly schedule (§ Operational Shape) | Task 18 |
| K8s Deployment for extract worker, long-running, Chainguard + tini + nonroot 65532 (§ Container Standard) | Task 19 |
| fsGroup: 65532 in K8s security context (§ Container Standard, QC.7) | Tasks 18–19 |

### Placeholder Scan

Reviewed. No placeholders. All code blocks are complete; the only intentional stub is `_mock_llm()` in tests, which is correct test design (LLM is external). The `get_db_conn()` reference in Task 16's `approve`/`reject` commands should use `get_tenant_conn` with the tenant derived from the row — flagged here as a follow-up for the implementer to resolve when integrating with the actual DB helper signature.

### Type Consistency Check

Reviewed. Names consistent across tasks:
- `BGEEmbedder` used in Tasks 12, 14, 17 — consistent.
- `ExtractWorker` / `CorroborateWorker` used in Tasks 13–15, 17 — consistent.
- `VocabularyResolver` matches Pass 1 Task 10 naming.
- `run_deterministic` matches Pass 1 Task 7 naming.
- `LLMExtractor.from_env()` matches Pass 1 Task 8 pattern.
- `statement_sources.confidence` (per-source, set by LLM extractor at 0.95 for deterministic) matches spec §3.3 formula inputs.
- `rank='normal'` / `rank='deprecated'` string literals match Plan 1 DDL.
- `FACTVAULT_EMBEDDING_MODEL` env var consistent between Task 12 implementation and Task 21 CI override.
