# Source Existence

> "If the original URL dies tomorrow, the captured text, archived snapshot, and content hash remain authoritative forever."

<!-- VISUAL: SVG diagram — source lifecycle states: collected → archived → extracted → verified → [live | link-rot | content-changed] -->

This document covers the source existence layer — Pillar 1 of the four architectural pillars and the reason factvault exists.

**Contents of this page are pending.** See the full specification for the complete treatment:

- [Design Spec §1 — The Promise](../superpowers/specs/2026-05-22-factvault-design.md#1-the-promise)
- [Design Spec §3.1 — Source Existence Layer](../superpowers/specs/2026-05-22-factvault-design.md#31-source-existence-layer)
- [Design Spec §4 §5 — Verify Stage](../superpowers/specs/2026-05-22-factvault-design.md#4-pipeline--the-six-stages)

Topics this page will cover when written:
- Why `raw_text` + `archive_url` + `content_hash` together (each alone is insufficient)
- The `source_verifications` append-only table and immutability trigger
- The verification cadence (7-day and 30-day rules)
- What happens when a URL goes dead (`link-rot` status, raw_text remains authoritative)
- The excerpt-offset check as the primary anti-hallucination mechanism
- How to query source verification history via the REST API (`GET /sources/{id}`)
