package workers

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/petersimmons1972/factvault/internal/testdb"
)

func TestCorroborateOnce_SucceedsWithContext(t *testing.T) {
	ctx := context.Background()

	err := CorroborateOnce(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCorroborateOnce_SucceedsWithCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Even with cancelled context, the placeholder should not error
	// Full implementation would respect context cancellation
	err := CorroborateOnce(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCorroboratorUpdatesConfidenceFromIndependentSources(t *testing.T) {
	ctx := context.Background()
	pool := testdb.Setup(ctx, t)
	defer pool.Close()
	tenantID := uuid.NewString()
	entityID := uuid.NewString()
	propertyID := uuid.NewString()
	statementID := uuid.NewString()
	sourceA := uuid.NewString()
	sourceB := uuid.NewString()
	setup := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO entities (id, tenant_id, label) VALUES ($1, $2, 'Acme')`, []any{entityID, tenantID}},
		{`INSERT INTO properties (id, tenant_id, slug, label, value_type) VALUES ($1, $2, 'source_note', 'Source note', 'string')`, []any{propertyID, tenantID}},
		{`INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, confidence) VALUES ($1, $2, $3, $4, 'note', 0.100)`, []any{statementID, tenantID, entityID, propertyID}},
		{`INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, publisher) VALUES ($1, $2, 'https://a.example/story', 'a', 'alpha body', 'verified', 'a.example')`, []any{sourceA, tenantID}},
		{`INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status, publisher) VALUES ($1, $2, 'https://b.example/story', 'b', 'beta body', 'verified', 'b.example')`, []any{sourceB, tenantID}},
		{`INSERT INTO statement_sources (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id) VALUES (gen_random_uuid(), $1, $2, 'alpha', 0, 5, 'test', 0.900, $3)`, []any{statementID, sourceA, tenantID}},
		{`INSERT INTO statement_sources (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id) VALUES (gen_random_uuid(), $1, $2, 'beta', 0, 4, 'test', 0.900, $3)`, []any{statementID, sourceB, tenantID}},
	}
	for _, stmt := range setup {
		if _, err := pool.Exec(ctx, stmt.query, stmt.args...); err != nil {
			t.Fatalf("setup: %v", err)
		}
	}
	if err := (&Corroborator{DB: pool}).CorroborateOnce(ctx, tenantID); err != nil {
		t.Fatalf("CorroborateOnce: %v", err)
	}
	var confidence float64
	if err := pool.QueryRow(ctx, `SELECT confidence::float8 FROM statements WHERE id=$1`, statementID).Scan(&confidence); err != nil {
		t.Fatalf("confidence: %v", err)
	}
	if confidence != 0.85 {
		t.Fatalf("confidence=%v want 0.85", confidence)
	}
}
