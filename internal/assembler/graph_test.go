package assembler

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

// graphTestTx begins a transaction with the given tenant set in the RLS GUC.
func graphTestTx(ctx context.Context, t *testing.T, pool *pgxpool.Pool, tenantID string) pgx.Tx {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	t.Cleanup(func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			t.Logf("rollback (expected after commit): %v", rbErr)
		}
	})
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		t.Fatalf("set tenant context: %v", err)
	}
	return tx
}

func insertEntity(ctx context.Context, t *testing.T, tx pgx.Tx, tenantID, extID, label string) string {
	t.Helper()
	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, now(), now())
	`, id, tenantID, extID, label, "https://schema.org/Thing"); err != nil {
		t.Fatalf("insert entity %s: %v", label, err)
	}
	return id
}

func insertRelation(ctx context.Context, t *testing.T, tx pgx.Tx, tenantID, sourceID, targetID, relType string, confidence float64) {
	t.Helper()
	if _, err := tx.Exec(ctx, `
		INSERT INTO relations (tenant_id, source_id, target_id, type, confidence)
		VALUES ($1, $2, $3, $4, $5)
	`, tenantID, sourceID, targetID, relType, confidence); err != nil {
		t.Fatalf("insert relation %s->%s: %v", sourceID, targetID, err)
	}
}

// TestExpandGraph_MultiHopRespectsDepthBound verifies that a 2-hop neighbor is
// reachable at depth 2 but NOT at depth 1. Graph: A -> B -> C (both edges conf 0.9).
func TestExpandGraph_MultiHopRespectsDepthBound(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	tx := graphTestTx(ctx, t, pool, tenantID)

	a := insertEntity(ctx, t, tx, tenantID, "graph-multihop-a", "EntityA")
	b := insertEntity(ctx, t, tx, tenantID, "graph-multihop-b", "EntityB")
	c := insertEntity(ctx, t, tx, tenantID, "graph-multihop-c", "EntityC")
	insertRelation(ctx, t, tx, tenantID, a, b, "related_to", 0.9)
	insertRelation(ctx, t, tx, tenantID, b, c, "related_to", 0.9)

	// Depth 1 from A: should reach A and B, but NOT C (2 hops away).
	d1, err := ExpandGraph(ctx, tx, []string{a}, 1, tenantID)
	if err != nil {
		t.Fatalf("ExpandGraph depth 1: %v", err)
	}
	if !slices.Contains(d1, a) || !slices.Contains(d1, b) {
		t.Errorf("depth 1: expected A and B reachable, got %v", d1)
	}
	if slices.Contains(d1, c) {
		t.Errorf("depth 1: C is 2 hops from A and must NOT appear, got %v", d1)
	}

	// Depth 2 from A: should reach A, B, and C.
	d2, err := ExpandGraph(ctx, tx, []string{a}, 2, tenantID)
	if err != nil {
		t.Fatalf("ExpandGraph depth 2: %v", err)
	}
	if !slices.Contains(d2, a) || !slices.Contains(d2, b) || !slices.Contains(d2, c) {
		t.Errorf("depth 2: expected A, B, and C all reachable, got %v", d2)
	}
}

// TestExpandGraph_CycleTerminatesWithDistinctEntities is the core test for the
// CTE rewrite: a mutual cycle A<->B must terminate and return DISTINCT entities
// with no duplicate rows. This validates the UNION set-semantics dedup.
func TestExpandGraph_CycleTerminatesWithDistinctEntities(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	tx := graphTestTx(ctx, t, pool, tenantID)

	a := insertEntity(ctx, t, tx, tenantID, "graph-cycle-a", "CycleA")
	b := insertEntity(ctx, t, tx, tenantID, "graph-cycle-b", "CycleB")
	// Mutual cycle: A -> B and B -> A, both above the confidence gate.
	insertRelation(ctx, t, tx, tenantID, a, b, "links_to", 0.9)
	insertRelation(ctx, t, tx, tenantID, b, a, "links_to", 0.9)

	// Depth 3 over a cycle must terminate (depth bound) and return DISTINCT IDs.
	got, err := ExpandGraph(ctx, tx, []string{a}, 3, tenantID)
	if err != nil {
		t.Fatalf("ExpandGraph over cycle: %v", err)
	}
	if !slices.Contains(got, a) || !slices.Contains(got, b) {
		t.Errorf("cycle: expected both A and B, got %v", got)
	}
	// ExpandGraph already SELECT DISTINCT, but assert no duplicate IDs leak out.
	seen := map[string]int{}
	for _, id := range got {
		seen[id]++
	}
	for id, n := range seen {
		if n > 1 {
			t.Errorf("cycle: entity %s appeared %d times, expected exactly 1 (no duplicate rows)", id, n)
		}
	}
	if len(got) != 2 {
		t.Errorf("cycle: expected exactly 2 distinct entities, got %d: %v", len(got), got)
	}
}

// TestExpandGraph_ConfidenceGateExcludesWeakEdges verifies an edge below the
// confidence gate (0.4) does NOT expand the graph.
func TestExpandGraph_ConfidenceGateExcludesWeakEdges(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	tenantID := uuid.NewString()
	tx := graphTestTx(ctx, t, pool, tenantID)

	a := insertEntity(ctx, t, tx, tenantID, "graph-conf-a", "ConfA")
	weak := insertEntity(ctx, t, tx, tenantID, "graph-conf-weak", "ConfWeak")
	strong := insertEntity(ctx, t, tx, tenantID, "graph-conf-strong", "ConfStrong")
	// A -> weak below gate (0.3); A -> strong above gate (0.5).
	insertRelation(ctx, t, tx, tenantID, a, weak, "maybe", 0.3)
	insertRelation(ctx, t, tx, tenantID, a, strong, "definitely", 0.5)

	got, err := ExpandGraph(ctx, tx, []string{a}, 1, tenantID)
	if err != nil {
		t.Fatalf("ExpandGraph: %v", err)
	}
	if !slices.Contains(got, a) || !slices.Contains(got, strong) {
		t.Errorf("expected A and strong neighbor reachable, got %v", got)
	}
	if slices.Contains(got, weak) {
		t.Errorf("weak edge (confidence 0.3 < 0.4 gate) must NOT expand; got %v", got)
	}
}
