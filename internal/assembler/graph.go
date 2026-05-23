package assembler

import (
	"context"
	"fmt"

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

	// TODO: Implement recursive CTE graph expansion
	// For now, return stub
	return nil, fmt.Errorf("ExpandGraph not yet implemented")
}
