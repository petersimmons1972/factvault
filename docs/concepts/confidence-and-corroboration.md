# Confidence and Corroboration

![Confidence Formula and Conflict Surface](../assets/svg/confidence-formula.svg)

Confidence is computed deterministically from independent source count. The LLM never sets confidence. No statement ever reaches 1.0 through the automated pipeline.

**Full treatment pending.** See the specification:

- [Design Spec §3.3 — Corroboration and Confidence](../superpowers/specs/2026-05-22-factvault-design.md#33-corroboration-and-confidence)

Topics this page will cover when written:
- The confidence formula (`compute_confidence()` in `factvault/assembler/confidence.py`)
- Independence criteria: same publisher domain (eTLD+1) OR trigram similarity ≥ 0.8 → not independent
- Why 0.95 is the automated ceiling (no statement auto-reaches 1.0)
- The `v_conflicts` view: what it surfaces and how to read it
- How to resolve a conflict: setting `rank='preferred'` and `rank='deprecated'`
- Why conflicts are preserved rather than silently resolved
- How to query active conflicts: `GET /conflicts`
