# Defining Properties

This is the one mandatory authoring task before your first `factvault` ingest. Define your property vocabulary before you ingest a single document, or you will spend time later merging `founded_in`, `founding year`, and `yearFounded` back into one canonical slug.

**Full guide pending.** See the specification:

- [Design Spec §8 — Documentation Strategy: defining-properties.md](../superpowers/specs/2026-05-22-factvault-design.md#8-documentation-strategy)
- [Design Spec §3.2 — Controlled Vocabulary](../superpowers/specs/2026-05-22-factvault-design.md#32-statement-model)

## Quick reference

```bash
# Register a property
factvault props create --slug raised_usd   --type number     --label "Raised (USD)"
factvault props create --slug ceo          --type entity_ref --label "Chief Executive Officer"
factvault props create --slug founded_in   --type date       --label "Founded"
factvault props create --slug headquarters --type string     --label "Headquarters"

# List registered properties
factvault props list

# Review LLM-proposed unknown slugs (strict mode)
factvault props review
```

## Value types

| Type | Use for |
|------|---------|
| `entity_ref` | Relationships between entities (ceo, acquired, competitor) |
| `string` | Text values (headquarters, status, description) |
| `number` | Numeric values (raised_usd, headcount, p_value) |
| `date` | Temporal values (founded_in, filed_at, published_at) |
| `url` | URLs that are themselves the value (homepage, sec_filing_url) |

Topics this page will cover when written:
- Why the controlled vocabulary exists (the silent-divergence failure mode)
- How to audit what facts you want to track before ingesting any documents
- Property definition examples for all four shipped example domains
- Strict vs. permissive mode and when each is appropriate
- How to merge duplicate properties discovered after ingestion
- The `proposed_properties` table and the review workflow
