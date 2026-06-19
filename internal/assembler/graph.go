package assembler

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// ExpandGraph executes a recursive CTE to find all entities within depth hops from seeds.
// Returns the full set of entity IDs (including seeds) reachable within depth.
// Edges are gated at confidence >= 0.4.
func ExpandGraph(
	ctx context.Context,
	tx pgx.Tx,
	seedEntityIDs []string,
	depth int,
	tenantID string,
) ([]string, error) {
	if depth < 0 || depth > 3 {
		return nil, ErrInvalidDepth
	}
	if len(seedEntityIDs) == 0 {
		return nil, ErrInvalidEntityCount
	}
	if depth == 0 {
		return uniqueStrings(seedEntityIDs), nil
	}

	// The original query had TWO recursive references (one per edge direction)
	// combined with UNION, which Postgres rejects: "recursive reference to query
	// must not appear within its non-recursive term". Both traversal directions
	// (source->target and target->source) are merged into a SINGLE inner subquery
	// so there is exactly one recursive reference, which Postgres accepts.
	//
	// The outer combiner is UNION (set semantics), not UNION ALL: with a single
	// recursive reference UNION is legal, and it dedups the intermediate frontier
	// on (entity_id, depth). On cyclic/dense graphs this keeps the frontier linear
	// in depth*|V| instead of the O(2^depth) blowup UNION ALL would produce; the
	// final result set is identical either way. The dedup identity is intentionally
	// the full (entity_id, depth) row — a node reached at multiple depths re-emits
	// once per depth level, which is correct for a depth-bounded traversal. Do not
	// add per-row accumulator columns (path arrays, running confidence) to the
	// recursive projection: that would defeat the dedup and could break termination.
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE graph(entity_id, depth) AS (
			SELECT unnest($1::uuid[]), 0
			UNION
			SELECT next_id, g.depth + 1
			FROM graph g
			JOIN (
				SELECT r.target_id AS next_id, r.source_id AS from_id, r.tenant_id, r.confidence
				FROM relations r
				UNION ALL
				SELECT r.source_id AS next_id, r.target_id AS from_id, r.tenant_id, r.confidence
				FROM relations r
			) r ON r.tenant_id = $2::uuid AND r.from_id = g.entity_id
			WHERE g.depth < $3 AND COALESCE(r.confidence, 0) >= 0.4
		)
		SELECT DISTINCT entity_id::text
		FROM graph
		ORDER BY entity_id::text
	`, uniqueStrings(seedEntityIDs), tenantID, depth)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entityIDs []string
	for rows.Next() {
		var entityID string
		if err := rows.Scan(&entityID); err != nil {
			return nil, err
		}
		entityIDs = append(entityIDs, entityID)
	}
	return entityIDs, rows.Err()
}
