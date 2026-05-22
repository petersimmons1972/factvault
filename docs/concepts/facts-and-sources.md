# Facts and Sources

A source is a document. A fact is a structured claim extracted from one or more documents. These are distinct things in factvault, and keeping them distinct is load-bearing: it is what allows a fact to accumulate multiple supporting sources over time, for contradictory evidence to be preserved rather than overwritten, and for every number in a generated report to trace back to the verbatim passage that produced it.

This document covers the statement model — Pillar 2 — and how the five schema objects (`entities`, `properties`, `statements`, `qualifiers`, `statement_sources`) work together to represent a research fact.

---

## Entities: named things in the world

Every fact is about a subject — a company, a person, a drug compound, a legislative bill. These subjects are `entities`. An entity has a `label` (human-readable name), an optional `ext_id` (an external canonical identifier, such as a Wikidata QID or an SEC CIK number), and an optional `type_uri` for interoperability with schema.org:

```sql
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    ext_id      TEXT,          -- e.g. 'Q12345', 'CIK:0001318605'
    label       TEXT NOT NULL,
    type_uri    TEXT,          -- schema.org URI; interop only, not structural schema
    description TEXT,
    embedding   vector(1024),  -- BGE-M3 embedding of label + description
    meta        JSONB NOT NULL DEFAULT '{}',
    ...
);
```

The `type_uri` field holds a schema.org URI — for example `https://schema.org/Organization` for a company, `https://schema.org/Person` for an individual — but this is for semantic interoperability only. It does not drive any database logic. The structural schema is defined by the `properties` table (see below), not by a third-party vocabulary.

---

## Properties: the controlled vocabulary of predicates

A statement asserts something about an entity using a predicate — `ceo`, `raised_usd`, `acquired_by`, `founded_in`. In factvault, predicates are called properties and they live in a controlled vocabulary:

```sql
CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,          -- NULL = system-wide; non-NULL = tenant-specific
    slug        TEXT NOT NULL, -- machine-readable; e.g. 'founded_in', 'ceo', 'raised_usd'
    label       TEXT NOT NULL, -- human-readable
    value_type  TEXT NOT NULL
                CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    UNIQUE (tenant_id, slug) NULLS NOT DISTINCT
);
```

The `value_type` field declares what kind of value the property holds. It has exactly five options:

| `value_type` | Use for | Example property |
|---|---|---|
| `entity_ref` | A relationship between two entities | `ceo`, `acquired_by`, `board_member` |
| `string` | Free text that is itself the value | `headquarters`, `ticker_symbol`, `product_name` |
| `number` | A numeric measurement or amount | `raised_usd`, `headcount`, `p_value` |
| `date` | A point or range in time | `founded_in`, `filed_at`, `ipo_date` |
| `url` | A URL that is itself the value (not a source) | `homepage`, `sec_filing_url`, `doi` |

This controlled vocabulary is what separates factvault's statement model from both key-value stores and pure EAV databases. In a key-value store, `"founded_in": "2017"` and `"founding_year": "2017"` and `"yearFounded": 2017` are three separate facts about the same predicate — the database has no way to know they are the same thing, and downstream queries silently miss records. In a pure EAV database, all predicates are strings with no type enforcement, which creates the same drift problem at scale. The `properties` table assigns each predicate a registered slug, a value type, and a unique-per-tenant identity before any data is written against it.

---

## Statements: the triple at the center

A statement is a `(subject, property, value)` triple. The `subject_id` is an entity. The `property_id` is a registered property. The value occupies exactly one of four typed columns (`val_entity`, `val_text`, `val_number`, `val_date`) — a CHECK constraint enforces that exactly one is non-NULL:

```sql
CONSTRAINT chk_statement_value_populated
    CHECK (
        (val_entity IS NOT NULL)::int +
        (val_text   IS NOT NULL)::int +
        (val_number IS NOT NULL)::int +
        (val_date   IS NOT NULL)::int = 1
    )
```

Statements carry two additional fields that are central to the data model:

- **`rank`** — `preferred`, `normal`, or `deprecated`. When two statements have the same `(subject_id, property_id)` with different values, both are preserved. The authoritative one is marked `preferred`; the superseded one `deprecated`. Neither is deleted. This is borrowed directly from Wikidata's rank model.
- **`confidence`** — a computed value in `[0.0, 1.0]` that reflects how many independent sources support this statement. It is never set by the LLM extractor. See [Confidence and Corroboration](confidence-and-corroboration.md) for the formula.

---

## Qualifiers: contextual metadata on statements

A raw triple is often insufficient for research use. "Acme Corp's CEO is Jane Smith" is a statement — but when? As of what date? Is this current or historical? Qualifiers answer these questions by attaching contextual metadata to a statement:

```sql
CREATE TABLE qualifiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_entity   UUID REFERENCES entities(id),
    CONSTRAINT chk_qualifier_value_populated
        CHECK (
            (val_entity IS NOT NULL)::int +
            (val_text   IS NOT NULL)::int +
            (val_number IS NOT NULL)::int +
            (val_date   IS NOT NULL)::int = 1
        )
);
```

A qualifier is itself a `(property, value)` pair — the same controlled vocabulary, the same typed value columns — but attached to a statement rather than an entity. Common qualifier properties include `start_time`, `end_time`, `point_in_time`, `deal_value_usd`, `deal_structure`, `jurisdiction`. The pattern is borrowed from Wikidata's qualifier model.

---

## How factvault implements this: a worked example

**The fact:** "Acme Corp acquired Beta Inc for $450M on April 12, 2023."

This is one statement with two qualifiers and a relation.

**Step 1 — Register entities.**

```sql
-- subject
INSERT INTO entities (id, tenant_id, label, type_uri, ext_id)
VALUES (
    'ent-acme-uuid', 'tenant-uuid',
    'Acme Corp', 'https://schema.org/Organization', 'CIK:0001234567'
);

-- value entity
INSERT INTO entities (id, tenant_id, label, type_uri)
VALUES (
    'ent-beta-uuid', 'tenant-uuid',
    'Beta Inc', 'https://schema.org/Organization', NULL
);
```

**Step 2 — Register properties** (if not already in the vocabulary).

```sql
INSERT INTO properties (tenant_id, slug, label, value_type)
VALUES
    ('tenant-uuid', 'acquired_by',      'Acquired by',       'entity_ref'),
    ('tenant-uuid', 'deal_value_usd',   'Deal value (USD)',   'number'),
    ('tenant-uuid', 'acquisition_date', 'Acquisition date',  'date');
```

**Step 3 — Write the statement.**

```sql
INSERT INTO statements (id, tenant_id, subject_id, property_id, val_entity, rank, confidence)
SELECT
    'stmt-acq-uuid',
    'tenant-uuid',
    'ent-beta-uuid',             -- subject: Beta Inc
    p.id,                        -- property: acquired_by
    'ent-acme-uuid',             -- value: Acme Corp (entity_ref)
    'normal',
    0.50                         -- initial value; confidence.py recomputes after corroboration
FROM properties p
WHERE p.tenant_id = 'tenant-uuid' AND p.slug = 'acquired_by';
```

**Step 4 — Attach qualifiers.**

```sql
-- deal value
INSERT INTO qualifiers (statement_id, property_id, val_number)
SELECT 'stmt-acq-uuid', p.id, 450000000
FROM properties p WHERE p.slug = 'deal_value_usd' AND p.tenant_id = 'tenant-uuid';

-- acquisition date
INSERT INTO qualifiers (statement_id, property_id, val_date)
SELECT 'stmt-acq-uuid', p.id, '2023-04-12'::timestamptz
FROM properties p WHERE p.slug = 'acquisition_date' AND p.tenant_id = 'tenant-uuid';
```

**Step 5 — Ground it in a source.**

```sql
INSERT INTO statement_sources (
    statement_id, source_id, excerpt,
    excerpt_offset_start, excerpt_offset_end,
    extraction_method, confidence
) VALUES (
    'stmt-acq-uuid',
    'src-reuters-uuid',
    'Acme Corp agreed to acquire Beta Inc for approximately $450 million',
    1842, 1905,
    'llm:claude-sonnet-4-6:v1',
    0.90
);
```

The excerpt offset check runs before this INSERT and confirms that `sources.raw_text[1842:1905]` matches the excerpt exactly (with minor whitespace normalization). If the LLM fabricated the excerpt, the offsets point nowhere and the INSERT is rejected.

The resulting data structure is fully self-describing: the fact (acquisition), its value ($450M, 2023-04-12, the acquiring entity), its supporting evidence (verbatim excerpt, offset-verified), and its provenance (which extraction method produced it) are all present and queryable without touching the original URL.
