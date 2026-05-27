# Defining Properties

This is the one mandatory authoring task before your first `factvault` ingest. Define your property vocabulary before you ingest a single document, or you will spend time later merging `founded_in`, `founding year`, and `yearFounded` back into one canonical slug. The merge is possible, but it requires re-running corroboration across all affected statements and has no automated rollback.

This guide covers what the controlled vocabulary is, how to register properties via SQL and Python, the complete `value_type` reference, strict vs. permissive mode, and the `proposed_properties` review workflow.

---

## What the controlled vocabulary is and why it matters

A `property` in factvault is a registered predicate: the middle term in a `(subject, property, value)` triple. The `properties` table is the gatekeeper for what facts can be expressed. A statement cannot be written using a predicate that does not exist as a `properties` row.

This design prevents two specific failure modes that make EAV-style databases unusable over time.

**Failure mode 1 — Free-text predicate soup.** In a system with no vocabulary control, three LLM extraction runs might produce `founded_in`, `founding_year`, and `yearFounded` as slugs for the same predicate. They land as three separate properties. Queries for any single slug miss the other two. Charts count statements per property and report three partial datasets. Aggregations are wrong. There is no automated way to detect that the three slugs are semantically identical, because the database treats them as distinct.

**Failure mode 2 — Schema.org as structural schema.** A common shortcut is to use schema.org URIs directly as property identifiers (`"https://schema.org/foundingDate"`, `"https://schema.org/numberOfEmployees"`). This creates a hard dependency on a vocabulary you cannot extend. schema.org has no slug for `raised_usd_series_a`, `sec_inquiry_outcome`, or `adverse_event_meddra_code`. When you need a predicate that schema.org does not define, you either invent a non-schema.org URI and break consistency, or you store the data in a structurally incompatible column. factvault uses schema.org URIs only in `entities.type_uri` for semantic interoperability — never as property identifiers.

The `properties` table solves both failure modes by requiring every predicate to be registered with a slug, a label, and a `value_type` before any statement can use it.

---

## How to register a new property

### Via SQL

```sql
INSERT INTO properties (tenant_id, slug, label, value_type, description)
VALUES (
    'a1b2c3d4-e5f6-7890-abcd-ef1234567890',  -- your tenant UUID
    'founded_by',
    'Founded by',
    'entity_ref',
    'The person or organization who founded this entity.'
);
```

For a numeric property:

```sql
INSERT INTO properties (tenant_id, slug, label, value_type, description)
VALUES (
    'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
    'acquired_in_usd',
    'Acquisition price (USD)',
    'number',
    'Total acquisition consideration in US dollars.'
);
```

For a string property:

```sql
INSERT INTO properties (tenant_id, slug, label, value_type, description)
VALUES (
    'a1b2c3d4-e5f6-7890-abcd-ef1234567890',
    'headquarters_location',
    'Headquarters location',
    'string',
    'City and country of primary headquarters (e.g. "San Francisco, CA, USA").'
);
```

The `UNIQUE (tenant_id, slug) NULLS NOT DISTINCT` constraint prevents duplicate slugs within a tenant. Attempting to insert a duplicate slug raises a unique violation at the database layer.

### Via Python (SQLAlchemy)

All application-layer writes require a `tenant_context` block (see [factvault/db/README.md](../factvault/db/README.md)):

```python
from uuid import UUID
from sqlalchemy import create_engine
from factvault.db.rls import tenant_context
from factvault.db.models import Property

engine = create_engine("postgresql+psycopg://factvault:factvault@localhost:5432/factvault")

TENANT_ID = UUID("a1b2c3d4-e5f6-7890-abcd-ef1234567890")

with engine.connect() as conn:
    with conn.begin():
        with tenant_context(conn, TENANT_ID):
            prop = Property(
                tenant_id=TENANT_ID,
                slug="founded_by",
                label="Founded by",
                value_type="entity_ref",
                description="The person or organization who founded this entity.",
            )
            conn.add(prop)
```

Both paths — SQL and Python — are equivalent. The SQL path is faster for bulk registration during initial setup. The Python path is appropriate for application-layer tooling that creates properties programmatically.

---

## The value_type enum

Every property has exactly one `value_type`. This determines which `val_*` column in `statements` holds the value, and it is enforced at INSERT time by a CHECK constraint.

| `value_type` | Meaning | Example property | Column used in `statements` |
|---|---|---|---|
| `entity_ref` | A relationship to another entity in the database | `ceo`, `acquired_by`, `board_member`, `founded_by` | `val_entity UUID` |
| `string` | Free text that is itself the value | `headquarters_location`, `ticker_symbol`, `product_name` | `val_text TEXT` |
| `number` | A numeric measurement or monetary amount | `raised_usd`, `headcount`, `p_value`, `acquired_in_usd` | `val_number NUMERIC` |
| `date` | A point in time (stored as `TIMESTAMPTZ`) | `founded_in`, `ipo_date`, `filed_at`, `acquisition_date` | `val_date TIMESTAMPTZ` |
| `url` | A URL that is itself the value, not the source of evidence | `homepage`, `sec_filing_url`, `clinicaltrials_url`, `doi` | `val_text TEXT` |

Note that both `string` and `url` use `val_text`. The `value_type` column records the semantic intent — a `url`-typed property is expected to contain a valid URL string; a `string`-typed property is expected to contain prose text. Neither is structurally validated at the Postgres layer beyond `val_text TEXT`.

**Choosing between `string` and `url`:** If the value will always be a clickable link (a homepage, a regulatory docket URL, a DOI), use `url`. If the value is a description, a name, a location, or any other human-readable text, use `string`.

**Choosing between `date` and `string`:** Prefer `date` for anything that represents a point or range in time. The `val_date TIMESTAMPTZ` column supports precise querying, ordering, and range filtering. Using `string` for dates forces downstream applications to parse the value.

---

## Strict vs. permissive mode

factvault operates in one of two modes, configured per deployment via the `FACTVAULT_PROPERTY_MODE` environment variable.

**Strict mode (default).** Any statement proposed with an unknown `property_slug` is rejected before INSERT. The proposed slug, inferred value type, and originating extraction method are written to `proposed_properties` for human review. The extraction worker logs the rejection and continues processing remaining statements from the document.

**Permissive mode** (`FACTVAULT_PROPERTY_MODE=permissive`). Any statement with an unknown slug causes an automatic INSERT into `properties` with `label = slug` and the inferred `value_type`. No human gate. Appropriate for rapid prototyping or when the vocabulary is intentionally open-ended.

The default is strict for a reason. Permissive mode is the faster path at the start of a project and the more dangerous path over time. Once `founded_in` and `founding_year` both auto-register, they are both valid properties with statements written against them. Merging them requires a migration.

The `proposed_properties` table:

```sql
CREATE TABLE proposed_properties (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    proposed_slug       TEXT NOT NULL,
    proposed_value_type TEXT NOT NULL CHECK (proposed_value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    proposed_by         TEXT NOT NULL,
    example_excerpt     TEXT,
    example_source_id   UUID REFERENCES sources(id),
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (tenant_id, proposed_slug, status)
);
```

A strict-mode rejection flow looks like this: extraction worker encounters `"yearFounded"` as a slug from an LLM extraction run → checks `properties` table → slug not found → INSERTs into `proposed_properties` with `status = 'pending'` → logs the rejection → statement is not written. The human reviews the queue and either approves (promoting to `properties`) or rejects.

---

## The proposed_properties review workflow

### Inspect pending proposals

```sql
SELECT proposed_slug, proposed_value_type, proposed_by, created_at
FROM proposed_properties
WHERE tenant_id = 'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
  AND status = 'pending'
ORDER BY created_at DESC;
```

### Approve a proposed property

Approving promotes the slug to the `properties` table and marks the proposal as reviewed:

```sql
BEGIN;

INSERT INTO properties (tenant_id, slug, label, value_type)
SELECT tenant_id, proposed_slug, proposed_slug, proposed_value_type
FROM proposed_properties
WHERE id = 'proposed-prop-uuid'
  AND tenant_id = 'a1b2c3d4-e5f6-7890-abcd-ef1234567890'
  AND status = 'pending';

UPDATE proposed_properties
SET status = 'approved', reviewed_by = 'operator', reviewed_at = now()
WHERE id = 'proposed-prop-uuid';

COMMIT;
```

After approval, the extraction worker can be re-run against documents that previously had this slug rejected. The rejected statements were not written; they will be re-extracted on the next pass.

### Reject a proposed property

```sql
UPDATE proposed_properties
SET status = 'rejected', reviewed_by = 'operator', reviewed_at = now()
WHERE id = 'proposed-prop-uuid'
  AND tenant_id = 'a1b2c3d4-e5f6-7890-abcd-ef1234567890';
```

Rejected proposals remain in the table with `status = 'rejected'`. They do not auto-retry. If the LLM proposes the same slug again, it can create a new `pending` row because uniqueness is scoped by `(tenant_id, proposed_slug, status)`.

---

## Best practices

- **Use snake_case slugs.** `acquired_by`, not `acquiredBy` or `Acquired_By`. The slug is a machine identifier; casing inconsistency creates the same silent-divergence problem as synonyms.
- **Choose `value_type` carefully.** Changing a property's `value_type` after statements have been written against it requires a migration: the old `val_*` column must be read, the value rewritten to the new column, and all downstream confidence values recomputed. There is no automated path for this.
- **Map entity types to schema.org URIs for interop.** Set `entities.type_uri = "https://schema.org/Organization"` for companies, `"https://schema.org/Person"` for people. This enables downstream consumers to interpret the entity type without knowing your internal schema. Do not use schema.org URIs as property slugs.
- **One property per (tenant, slug).** The unique constraint enforces this at the database layer. If you find yourself wanting to create `raised_usd_series_a` and `raised_usd_series_b` as separate properties, reconsider: `raised_usd` with a `round_type` qualifier on the statement is almost always the correct model.
- **Never reuse a slug across semantically distinct meanings.** If `status` meant "regulatory approval status" in Q1 and you want to add "employment status" for person entities, register a distinct property (`employment_status`). Reusing `status` for two meanings creates ambiguous queries.

---

## Common pitfalls

**Forgetting `tenant_id`.** The `tenant_id` column is `NOT NULL` in `properties`. An INSERT without it fails with a not-null violation. The `tenant_context()` context manager does not automatically supply `tenant_id` to INSERT statements — you must include it explicitly in the column list.

**Mixing value types within one slug.** If two extraction runs propose `headcount` with `value_type = 'number'` and `value_type = 'string'` respectively, the second proposal will conflict at approval time. The `properties` table allows only one `value_type` per slug. Decide the canonical type at registration time and hold to it.

**Allowing the LLM extractor to auto-create slugs in production.** Permissive mode is a prototyping convenience. In production, all deployments should run strict mode. The cost of a human reviewing 20 proposed properties before they are registered is far lower than the cost of discovering, six months later, that your database contains 40 synonymous slugs with statements split across them.
