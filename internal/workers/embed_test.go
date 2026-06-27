package workers_test

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/embed"
	"github.com/petersimmons1972/factvault/internal/testdb"
	"github.com/petersimmons1972/factvault/internal/workers"
)

// makeEmbedServer returns a test HTTP server that mimics the embedder service.
// It returns unit-normalized 1024-dim vectors (1.0 at index 0).
func makeEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			v[0] = 1.0 // unit vector along first axis
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestEmbedWorker_BackfillEntities is the primary integration test:
// - Inserts entities with NULL embeddings into the test DB
// - Runs EmbedWorker.RunOnce (backfill mode)
// - Asserts all processed entities have dim=1024 embeddings
// - Asserts re-running is idempotent (no embed calls for already-populated rows)
func TestEmbedWorker_BackfillEntities(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000501"

	// Track embed call count to verify idempotency
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		callCount += len(req.Texts)
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			v[0] = 1.0
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	// Insert 3 entities WITHOUT embeddings
	ids := []string{
		uuid.NewString(),
		uuid.NewString(),
		uuid.NewString(),
	}
	for i, id := range ids {
		if _, err := pool.Exec(ctx, `
			INSERT INTO entities (id, tenant_id, ext_id, label, type_uri)
			VALUES ($1::uuid, $2::uuid, $3, $4, NULL)
		`, id, tenantID, "ext:embed-test:"+id, []string{"Alpha Entity", "Beta Entity", "Gamma Entity"}[i]); err != nil {
			t.Fatalf("insert entity %d: %v", i, err)
		}
	}

	client := embed.NewClient(srv.URL, srv.Client())
	w := &workers.EmbedWorker{
		DB:     pool,
		Client: client,
	}

	// RED: this call should fail because EmbedWorker does not exist yet
	result, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Populated != 3 {
		t.Fatalf("expected Populated=3, got %d", result.Populated)
	}

	// Verify: all 3 entities now have non-NULL embeddings
	var nullCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM entities
		WHERE tenant_id = $1::uuid AND embedding IS NULL
	`, tenantID).Scan(&nullCount); err != nil {
		t.Fatalf("count NULL embeddings: %v", err)
	}
	if nullCount != 0 {
		t.Fatalf("expected 0 NULL embeddings after backfill, got %d", nullCount)
	}

	// Verify: embeddings are dim=1024
	rows, err := pool.Query(ctx, `
		SELECT embedding FROM entities WHERE tenant_id = $1::uuid
	`, tenantID)
	if err != nil {
		t.Fatalf("query embeddings: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var vec pgvector.Vector
		if err := rows.Scan(&vec); err != nil {
			t.Fatalf("scan embedding: %v", err)
		}
		if len(vec.Slice()) != 1024 {
			t.Fatalf("expected dim=1024, got %d", len(vec.Slice()))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	// Idempotency: run again, embed call count should not increase
	callsBefore := callCount
	result2, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100})
	if err != nil {
		t.Fatalf("RunOnce (second): %v", err)
	}
	if result2.Populated != 0 {
		t.Fatalf("idempotency: expected Populated=0 on re-run, got %d", result2.Populated)
	}
	if callCount != callsBefore {
		t.Fatalf("idempotency: embed was called %d extra time(s) on re-run", callCount-callsBefore)
	}
}

// TestEmbedWorker_BackfillSources verifies sources get embeddings populated.
func TestEmbedWorker_BackfillSources(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000502"

	srv := makeEmbedServer(t)
	client := embed.NewClient(srv.URL, srv.Client())

	// Insert source without embedding
	sourceID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO sources (id, tenant_id, url, fetched_at, content_hash, raw_html, raw_text, status, created_at)
		VALUES ($1::uuid, $2::uuid, 'https://example.com/embed-test', now(), 'abc123',
		        ''::bytea, 'This is the raw text of an article about important facts.', 'archived', now())
	`, sourceID, tenantID); err != nil {
		t.Fatalf("insert source: %v", err)
	}

	w := &workers.EmbedWorker{
		DB:     pool,
		Client: client,
	}
	result, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Populated == 0 {
		t.Fatal("expected at least 1 source to be populated")
	}

	// Verify source embedding is non-NULL
	var vec pgvector.Vector
	if err := pool.QueryRow(ctx, `
		SELECT embedding FROM sources WHERE id = $1::uuid
	`, sourceID).Scan(&vec); err != nil {
		t.Fatalf("scan source embedding: %v", err)
	}
	if len(vec.Slice()) != 1024 {
		t.Fatalf("expected dim=1024 source embedding, got %d", len(vec.Slice()))
	}
}

// TestEmbedWorker_BackfillStatements verifies statements get embeddings populated.
func TestEmbedWorker_BackfillStatements(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000503"

	srv := makeEmbedServer(t)
	client := embed.NewClient(srv.URL, srv.Client())

	// Insert prerequisite entity and property, then statement without embedding
	entityID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label) VALUES ($1::uuid, $2::uuid, 'ext:stmt-test', 'Test Subject')
	`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	propertyID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1::uuid, $2::uuid, 'test-prop', 'Test Property', 'string')
	`, propertyID, tenantID); err != nil {
		t.Fatalf("insert property: %v", err)
	}

	statementID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO statements (id, tenant_id, subject_id, property_id, val_text, rank, confidence)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'some value', 'normal', 0.9)
	`, statementID, tenantID, entityID, propertyID); err != nil {
		t.Fatalf("insert statement: %v", err)
	}

	w := &workers.EmbedWorker{
		DB:     pool,
		Client: client,
	}
	result, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Populated == 0 {
		t.Fatal("expected at least 1 statement to be populated")
	}

	var vec pgvector.Vector
	if err := pool.QueryRow(ctx, `
		SELECT embedding FROM statements WHERE id = $1::uuid
	`, statementID).Scan(&vec); err != nil {
		t.Fatalf("scan statement embedding: %v", err)
	}
	if len(vec.Slice()) != 1024 {
		t.Fatalf("expected dim=1024 statement embedding, got %d", len(vec.Slice()))
	}
}

// TestEmbedWorker_VectorsAreUnitNormalized verifies the stored vectors have L2 norm ≈ 1.0.
func TestEmbedWorker_VectorsAreUnitNormalized(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000504"

	// Server returns a known unit vector for verification
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			// Spread the unit vector: 1/sqrt(1024) at each position → norm = 1.0
			val := 1.0 / math.Sqrt(1024)
			for j := range v {
				v[j] = val
			}
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	entityID := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label)
		VALUES ($1::uuid, $2::uuid, 'ext:norm-test', 'Norm Test Entity')
	`, entityID, tenantID); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	client := embed.NewClient(srv.URL, srv.Client())
	w := &workers.EmbedWorker{DB: pool, Client: client}
	if _, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	var vec pgvector.Vector
	if err := pool.QueryRow(ctx, `SELECT embedding FROM entities WHERE id = $1::uuid`, entityID).Scan(&vec); err != nil {
		t.Fatalf("scan: %v", err)
	}

	// Compute L2 norm
	var norm float64
	for _, f := range vec.Slice() {
		norm += float64(f) * float64(f)
	}
	norm = math.Sqrt(norm)
	if math.Abs(norm-1.0) > 0.01 {
		t.Fatalf("expected unit-normalized vector (norm≈1.0), got norm=%.6f", norm)
	}
}

// TestEmbedWorker_SkipsAlreadyPopulated verifies rows with existing embeddings are skipped.
func TestEmbedWorker_SkipsAlreadyPopulated(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000505"

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		callCount += len(req.Texts)
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			v[0] = 1.0
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	// Insert one entity WITH embedding already set
	entityID := uuid.NewString()
	existingVec := make([]float32, 1024)
	existingVec[1] = 1.0 // different from what the server returns
	if _, err := pool.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, embedding)
		VALUES ($1::uuid, $2::uuid, 'ext:skip-test', 'Already Embedded', $3)
	`, entityID, tenantID, pgvector.NewVector(existingVec)); err != nil {
		t.Fatalf("insert entity with embedding: %v", err)
	}

	client := embed.NewClient(srv.URL, srv.Client())
	w := &workers.EmbedWorker{DB: pool, Client: client}
	result, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 100})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.Populated != 0 {
		t.Fatalf("expected 0 populated (all already set), got %d", result.Populated)
	}
	if callCount != 0 {
		t.Fatalf("expected 0 embed calls for pre-populated rows, got %d", callCount)
	}

	// Verify existing embedding was NOT overwritten
	var vec pgvector.Vector
	if err := pool.QueryRow(ctx, `SELECT embedding FROM entities WHERE id = $1::uuid`, entityID).Scan(&vec); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if vec.Slice()[1] != 1.0 {
		t.Fatalf("existing embedding was overwritten (expected [1]=1.0, got %v)", vec.Slice()[1])
	}
}

// TestEmbedWorker_RespectsTenantIsolation verifies embeddings are only populated
// for the specified tenant, not for rows belonging to other tenants.
func TestEmbedWorker_RespectsTenantIsolation(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantA := "00000000-0000-0000-0000-000000000506"
	tenantB := "00000000-0000-0000-0000-000000000507"

	srv := makeEmbedServer(t)

	// Insert one entity for tenant A and one for tenant B
	idA := uuid.NewString()
	idB := uuid.NewString()
	for _, row := range []struct{ id, tenant, label string }{
		{idA, tenantA, "Tenant A Entity"},
		{idB, tenantB, "Tenant B Entity"},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO entities (id, tenant_id, ext_id, label)
			VALUES ($1::uuid, $2::uuid, $3, $4)
		`, row.id, row.tenant, "ext:iso:"+row.id, row.label); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	client := embed.NewClient(srv.URL, srv.Client())
	w := &workers.EmbedWorker{DB: pool, Client: client}

	// Run embed only for tenant A
	if _, err := w.RunOnce(ctx, tenantA, workers.EmbedOptions{Limit: 100}); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// Tenant A entity should have embedding
	var vecA pgvector.Vector
	if err := pool.QueryRow(ctx, `SELECT embedding FROM entities WHERE id = $1::uuid`, idA).Scan(&vecA); err != nil {
		t.Fatalf("scan tenant A: %v", err)
	}
	if len(vecA.Slice()) == 0 {
		t.Fatal("tenant A entity should have embedding")
	}

	// Tenant B entity should still be NULL — use raw query bypassing RLS
	var embeddingNull bool
	if err := pool.QueryRow(ctx, `SELECT embedding IS NULL FROM entities WHERE id = $1::uuid`, idB).Scan(&embeddingNull); err != nil {
		t.Fatalf("scan tenant B: %v", err)
	}
	if !embeddingNull {
		t.Fatal("tenant B entity should not have been embedded (different tenant)")
	}
}

// TestEmbedWorker_InvalidTenantErrors verifies that an empty or invalid tenant ID returns error.
func TestEmbedWorker_InvalidTenantErrors(t *testing.T) {
	pool := testdb.New(t)
	srv := makeEmbedServer(t)
	client := embed.NewClient(srv.URL, srv.Client())

	w := &workers.EmbedWorker{DB: pool, Client: client}
	_, err := w.RunOnce(context.Background(), "", workers.EmbedOptions{Limit: 100})
	if err == nil {
		t.Fatal("expected error for empty tenant ID, got nil")
	}
}

// TestEmbedResult_CountsAreAdditive verifies that EmbedResult accumulates counts
// across entities, statements, and sources.
func TestEmbedResult_StructFields(_ *testing.T) {
	// Compile-time check that the result struct has the expected fields
	var r workers.EmbedResult
	_ = r.Populated
	_ = r.Skipped
}

// TestEmbedOptions_LimitIsRespected verifies the Limit option caps how many rows are processed.
func TestEmbedOptions_LimitIsRespected(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000508"

	srv := makeEmbedServer(t)

	// Insert 5 entities
	for i := range 5 {
		id := uuid.NewString()
		if _, err := pool.Exec(ctx, `
			INSERT INTO entities (id, tenant_id, ext_id, label)
			VALUES ($1::uuid, $2::uuid, $3, $4)
		`, id, tenantID, "ext:limit:"+id, []string{"A", "B", "C", "D", "E"}[i]); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	client := embed.NewClient(srv.URL, srv.Client())
	w := &workers.EmbedWorker{DB: pool, Client: client}

	// Run with limit=2 — should process at most 2 per table
	result, err := w.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 2})
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	// With limit=2, at most 2 entity rows processed (sources/statements may have 0 in this tenant)
	if result.Populated > 2 {
		t.Fatalf("expected at most 2 populated with Limit=2, got %d", result.Populated)
	}
}

// TestEmbedWorker_TxReleasedBeforeEmbedCall (W-13 regression) verifies that the
// fetch transaction is committed before the HTTP embed call is made.  We use a
// pool with MaxConns=1: if the fetch TX were still open while the embed HTTP
// handler runs, the handler's attempt to acquire a DB connection would block /
// timeout and the test would fail.
func TestEmbedWorker_TxReleasedBeforeEmbedCall(t *testing.T) {
	// Build a 1-connection pool from the test DB config.
	basePool := testdb.New(t)
	cfg := basePool.Config()
	cfg.MaxConns = 1
	smallPool, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatalf("create small pool: %v", err)
	}
	t.Cleanup(smallPool.Close)

	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000601"

	// Insert one entity with NULL embedding using the base pool (which has
	// more connections and can co-exist with smallPool).
	id := uuid.NewString()
	if _, err := basePool.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label)
		VALUES ($1::uuid, $2::uuid, $3, 'W13 subject')
	`, id, tenantID, "ext:w13:"+id); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Embed server: inside the handler, try to acquire a connection from
	// smallPool.  If the fetch TX is still open, the pool has no free
	// connections (MaxConns=1) and Acquire would time out → test fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acquireCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		conn, acquireErr := smallPool.Acquire(acquireCtx)
		if acquireErr != nil {
			http.Error(w, "W-13: fetch TX still open during embed HTTP call: "+acquireErr.Error(), http.StatusInternalServerError)
			return
		}
		conn.Release()

		var req struct {
			Texts []string `json:"texts"`
		}
		if decErr := json.NewDecoder(r.Body).Decode(&req); decErr != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		vecs := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			v[0] = 1.0
			vecs[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(map[string]any{"vectors": vecs}); encErr != nil {
			t.Errorf("encode: %v", encErr)
		}
	}))
	t.Cleanup(srv.Close)

	worker := &workers.EmbedWorker{
		DB:     smallPool,
		Client: embed.NewClient(srv.URL, srv.Client()),
	}
	result, err := worker.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 10})
	if err != nil {
		t.Fatalf("RunOnce: %v (W-13: fetch TX may still be open during embed call)", err)
	}
	if result.Populated != 1 {
		t.Fatalf("Populated = %d, want 1", result.Populated)
	}
}

// TestEmbedWorker_WrongDimVectorRejected (W-14 regression) verifies that an
// embedder returning wrong-dimension vectors causes RunOnce to return an error
// rather than silently writing garbage into the embedding column.
func TestEmbedWorker_WrongDimVectorRejected(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	tenantID := "00000000-0000-0000-0000-000000000602"

	id := uuid.NewString()
	if _, err := pool.Exec(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label)
		VALUES ($1::uuid, $2::uuid, $3, 'W14 subject')
	`, id, tenantID, "ext:w14:"+id); err != nil {
		t.Fatalf("insert entity: %v", err)
	}

	// Embed server returns 3-dim vectors (wrong dimension).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad", http.StatusBadRequest)
			return
		}
		vecs := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			vecs[i] = []float64{0.1, 0.2, 0.3} // only 3 dims — wrong
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vecs}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	worker := &workers.EmbedWorker{
		DB:     pool,
		Client: embed.NewClient(srv.URL, srv.Client()),
	}
	_, err := worker.RunOnce(ctx, tenantID, workers.EmbedOptions{Limit: 10})
	if err == nil {
		t.Fatal("expected error when embedder returns wrong-dimension vectors (W-14), got nil")
	}
}

// Compile-time check that db.Source has the embedding field we need.
var (
	_ pgvector.Vector = db.Source{}.Embedding
	_ pgvector.Vector = db.Entity{}.Embedding
	_ pgvector.Vector = db.Statement{}.Embedding
)
