package retrieval_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/embed"
	"github.com/petersimmons1972/factvault/internal/retrieval"
	"github.com/petersimmons1972/factvault/internal/store/postgres"
	"github.com/petersimmons1972/factvault/internal/testdb"
)

// testTenantID is used across all retrieval service tests.
const testTenantID = "00000000-0000-0000-0000-000000000201"

// TestMain starts the Postgres container once for this package.
func TestMain(m *testing.M) {
	testdb.StartContainer()
	code := m.Run()
	testdb.StopContainer()
	os.Exit(code)
}

// makeEmbedServer returns an httptest server that returns a fixed 1024-dim unit vector
// along the first axis for any texts sent to POST /embed.
func makeEmbedServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" || r.Method != http.MethodPost {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		vec := make([]float64, 1024)
		vec[0] = 1.0 // unit vector along first axis
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, 1024)
			v[0] = 1.0
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if encErr := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); encErr != nil {
			http.Error(w, "encode error", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// unitVec1024 returns a 1024-dim unit vector along the first axis.
// Used to pre-populate entity embeddings so cosine similarity to the
// query vector returned by makeEmbedServer is 1.0 (identical).
func unitVec1024() []float32 {
	v := make([]float32, 1024)
	v[0] = 1.0
	return v
}

// TestSeedEntities_CosineFindsByEmbeddingSimilarity is the key TDD test:
// it inserts an entity whose label has NO lexical overlap with the query,
// but whose embedding is identical to the query embedding returned by the
// test embed server. The entity MUST be found by cosine search and MUST
// NOT be found by the old ILIKE path.
func TestSeedEntities_CosineFindsByEmbeddingSimilarity(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	// Insert entity with a deliberately opaque label ("XR-7000") — no overlap
	// with any plausible test query text ("acme corporation" etc.).
	// Its embedding is a unit vector along axis 0.
	_, err := pool.Exec(ctx,
		`INSERT INTO entities (tenant_id, ext_id, label, embedding)
		 VALUES ($1::uuid, 'cosine-test-unique-entity', 'XR-7000', $2)`,
		testTenantID, pgvector.NewVector(unitVec1024()),
	)
	if err != nil {
		t.Fatalf("insert test entity: %v", err)
	}

	embedSrv := makeEmbedServer(t)
	embedClient := embed.NewClient(embedSrv.URL, embedSrv.Client())
	pgStore := postgres.New(pool)

	svc := retrieval.NewService(pool, embedClient, pgStore)

	// Query text has zero lexical overlap with "XR-7000".
	// Under the old ILIKE path this would return nothing.
	// Under cosine search, the embed server returns a unit vector along axis 0,
	// matching the entity's embedding exactly (score = 1.0 > 0.6 threshold).
	resp, err := svc.Story(ctx, testTenantID, retrieval.StoryRequest{Query: "anthropic language models"})
	if err != nil {
		t.Fatalf("Story: unexpected error: %v", err)
	}
	if resp == nil || resp.Bundle == nil {
		t.Fatal("Story: expected non-nil bundle, got nil")
	}

	found := false
	for _, e := range resp.Bundle.Entities {
		if e.Name == "XR-7000" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Story: expected cosine seed-search to find 'XR-7000' entity, bundle entities: %+v", resp.Bundle.Entities)
	}
}

// TestSeedEntities_FallsBackToILIKEWhenEmbedderUnreachable verifies that
// a down embedder does NOT cause a 500. ILIKE path must serve the request.
func TestSeedEntities_FallsBackToILIKEWhenEmbedderUnreachable(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	// Insert entity with a label that ILIKE can find.
	_, err := pool.Exec(ctx,
		`INSERT INTO entities (tenant_id, ext_id, label, embedding)
		 VALUES ($1::uuid, 'fallback-test-entity', 'Fallback Corp', $2)`,
		testTenantID, pgvector.NewVector(unitVec1024()),
	)
	if err != nil {
		t.Fatalf("insert test entity: %v", err)
	}

	// Point the embed client at a server that immediately closes connections.
	deadSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, hijackErr := hj.Hijack()
		if hijackErr != nil {
			return
		}
		if closeErr := conn.Close(); closeErr != nil {
			return
		}
	}))
	t.Cleanup(deadSrv.Close)

	embedClient := embed.NewClient(deadSrv.URL, deadSrv.Client())
	pgStore := postgres.New(pool)

	svc := retrieval.NewService(pool, embedClient, pgStore)

	// The query text matches "Fallback Corp" via ILIKE.
	// With the embedder down, we must fall back gracefully — no error.
	resp, err := svc.Story(ctx, testTenantID, retrieval.StoryRequest{Query: "Fallback"})
	if err != nil {
		t.Fatalf("Story with dead embedder: expected graceful fallback, got error: %v", err)
	}
	if resp == nil || resp.Bundle == nil {
		t.Fatal("Story with dead embedder: expected non-nil bundle, got nil")
	}
}

// TestSeedEntities_EmptyQueryReturnsRecentEntities verifies that the empty-query
// behavior (return recent entities, no filtering) is unchanged.
func TestSeedEntities_EmptyQueryReturnsRecentEntities(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	// Insert a single entity — empty query must still return it.
	_, err := pool.Exec(ctx,
		`INSERT INTO entities (tenant_id, ext_id, label)
		 VALUES ($1::uuid, 'empty-query-test-entity', 'Empty Query Entity')`,
		testTenantID,
	)
	if err != nil {
		t.Fatalf("insert test entity: %v", err)
	}

	embedSrv := makeEmbedServer(t)
	embedClient := embed.NewClient(embedSrv.URL, embedSrv.Client())
	pgStore := postgres.New(pool)

	svc := retrieval.NewService(pool, embedClient, pgStore)

	// Empty query — must not call embedder and must return recent entities.
	resp, err := svc.Story(ctx, testTenantID, retrieval.StoryRequest{Query: ""})
	if err != nil {
		t.Fatalf("Story with empty query: unexpected error: %v", err)
	}
	if resp == nil || resp.Bundle == nil {
		t.Fatal("Story with empty query: expected non-nil bundle")
	}
}
