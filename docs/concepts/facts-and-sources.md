# Facts and Sources

<!-- VISUAL: SVG entity-relationship diagram — entities → statements → statement_sources → sources; qualifiers hanging off statements -->

This document covers the statement model — Pillar 2 of the four architectural pillars.

**Contents pending.** Full specification:

- [Design Spec §3.2 — Statement Model](../superpowers/specs/2026-05-22-factvault-design.md#32-statement-model)

Topics this page will cover when written:
- How a fact (statement) differs from a source
- The `(subject, property, value)` triple and why exactly one value column is populated
- Qualifiers: contextual metadata on statements (point_in_time, deal_value_usd, etc.)
- What excerpt offsets mean: character-level offsets into `sources.raw_text`, verified before INSERT
- The `rank` enum: preferred / normal / deprecated
- The controlled property vocabulary and why free-text predicates destroy database quality over time
- Strict vs. permissive mode — when to use each
- How to register a property: `factvault props create`
