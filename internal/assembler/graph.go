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

	// Two separate recursive parts (target_id + source_id) cannot both reference the
	// CTE under UNION in Postgres — only UNION ALL is allowed in recursive terms that
	// contain multiple recursive references (PG error: "recursive reference to query
	// must not appear within its non-recursive term"). Merge the two traversal
	// directions with UNION ALL inside a subquery, then DISTINCT-select at the top.
	rows, err := tx.Query(ctx, `
		WITH RECURSIVE graph(entity_id, depth) AS (
			SELECT unnest($1::uuid[]), 0
			UNION ALL
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
