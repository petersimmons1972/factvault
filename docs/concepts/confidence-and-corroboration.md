# Confidence and Corroboration

Confidence in factvault is a number computed by a deterministic formula, not a value guessed by a language model. This distinction is non-negotiable: any system that allows the extractor to set its own confidence produces a number that reflects the model's calibration (or overconfidence), not the actual state of the evidence. factvault's confidence reflects one thing — how many independent sources support a statement.

---

## Per-source confidence vs. statement-level confidence

Two separate confidence values appear in the data model.

**Per-source confidence** (`statement_sources.confidence`) is set by the extraction worker when it writes a `statement_sources` row. It reflects the extraction quality: how clearly the excerpt supports the statement, the reliability of the extraction method, and whether the extraction was machine-produced or human-reviewed. A well-matched excerpt from a structured regulatory filing might score 0.95; an ambiguous passage from a blog post might score 0.65. These values are inputs to the corroboration formula, not the output.

**Statement-level confidence** (`statements.confidence`) is computed by `factvault/assembler/confidence.py` and reflects independent source count with a ceiling applied by the per-source maximum. The formula:

```python
def compute_confidence(statement_id: str, tenant_id: str) -> float:
    sources = get_sources_for_statement(statement_id, tenant_id)

    if not sources:
        return 0.0

    independent_groups = _cluster_by_independence(sources)
    n = len(independent_groups)

    per_source_max = max(s.confidence for s in sources if s.confidence is not None) \
        if any(s.confidence is not None for s in sources) else 1.0

    if n == 1:
        return min(per_source_max, 0.50)
    elif n == 2:
        return min(per_source_max, 0.85)
    else:  # n >= 3
        return min(per_source_max, 0.95)
```

The LLM never calls this function and never writes to `statements.confidence` directly. The corroborate worker owns confidence recomputation.

---

## The confidence ceilings

| Independent sources | Statement-level confidence ceiling |
|---|---|
| 0 | 0.0 |
| 1 | 0.50 |
| 2 | 0.85 |
| ≥ 3 | 0.95 |

The ceiling of 0.95 is the maximum achievable through automated corroboration, regardless of how many independent sources are present. No statement reaches 1.0 through the automated pipeline. A confidence of 0.95 means: three or more independent sources agree, and the per-source extraction quality supports this ceiling. It does not mean "certainty." The 5% gap is structural — it preserves space for human review and acknowledges that automated extraction is not infallible.

A human can set `rank = 'preferred'` and manually update `confidence` above 0.95 for specific statements after editorial review. This is an explicit act of human override, not an automated pipeline output.

---

## Independence: the precise definition

Two sources are not independent if either condition holds:

1. **Same publisher domain.** The eTLD+1 of the `publisher` field is identical. `reuters.com` and `uk.reuters.com` share the eTLD+1 `reuters.com` — not independent. `reuters.com` and `thomsonreuters.com` are different eTLD+1 values — independent (unless the content similarity check triggers below).

2. **High trigram similarity.** The trigram similarity of `source_a.raw_text[:2000]` vs `source_b.raw_text[:2000]` is ≥ 0.8. The 2000-character prefix captures the article lede, which is the portion most commonly syndicated verbatim. If two sources from different publishers have lede similarity ≥ 0.8, they are treated as the same independent group regardless of publisher domain. This catches wire copy that ran under different outlet mastheads.

The `_cluster_by_independence` function builds clusters of non-independent sources and counts the number of clusters. That count is `n` in the formula above. Each cluster contributes at most one independent voice.

---

## The rank enum and conflict handling

Statements carry a `rank` column with three values: `preferred`, `normal`, and `deprecated`.

When two statements have the same `(subject_id, property_id)` but different values, they coexist in the database. Neither is deleted. The `v_conflicts` SQL view surfaces these automatically:

```sql
CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    s1.id           AS statement_a_id,
    s2.id           AS statement_b_id,
    s1.subject_id,
    s1.property_id,
    p.slug          AS property_slug,
    s1.val_text     AS val_a_text,
    s1.val_number   AS val_a_number,
    s1.val_date     AS val_a_date,
    s1.val_entity   AS val_a_entity,
    s2.val_text     AS val_b_text,
    ...
    s1.confidence   AS confidence_a,
    s2.confidence   AS confidence_b,
    s1.rank         AS rank_a,
    s2.rank         AS rank_b
FROM statements s1
JOIN statements s2
    ON  s1.subject_id  = s2.subject_id
    AND s1.property_id = s2.property_id
    AND s1.id < s2.id
    AND s1.rank != 'deprecated'
    AND s2.rank != 'deprecated'
JOIN properties p ON p.id = s1.property_id
WHERE
    (s1.val_text   IS DISTINCT FROM s2.val_text)   OR
    (s1.val_number IS DISTINCT FROM s2.val_number) OR
    (s1.val_date   IS DISTINCT FROM s2.val_date)   OR
    (s1.val_entity IS DISTINCT FROM s2.val_entity);
```

`v_conflicts` shows every pair of non-deprecated statements on the same subject and property that disagree on value. The bundle assembler includes conflicting statements in the `conflicts[]` block of any bundle that touches the relevant entities, so downstream LLMs see the disagreement rather than receiving a silently-resolved single value.

To resolve a conflict: set `rank = 'preferred'` on the authoritative statement and `rank = 'deprecated'` on the superseded one. The deprecated statement remains in the database. Its evidence and source history are preserved.

---

## Why the LLM never sets confidence

An LLM extractor that produces its own confidence scores for the facts it extracts is grading its own homework. There is no structural constraint preventing it from returning 0.95 for a hallucinated claim. Even a well-calibrated model cannot know whether the excerpt it produced will survive the offset check against `raw_text`, whether the source will still be live in six months, or whether two other sources already corroborate or contradict the claim.

factvault enforces a hard separation: the extractor produces `(excerpt, offset_start, offset_end, per_source_confidence)`. The per-source confidence is an extraction quality estimate — useful for the formula's ceiling, not for setting the final statement confidence. The statement-level confidence is computed after extraction, by a separate worker (`workers/corroborate.py`), using only the structural evidence: how many independent sources survived the offset check and were written into `statement_sources`.

This separation is what makes the confidence column trustworthy across a dataset. Every statement's confidence value was computed by the same deterministic formula from the same structural evidence. There is no per-model calibration variance, no prompt-injection risk, and no way for an LLM to inflate confidence on claims that benefit from appearing authoritative.
