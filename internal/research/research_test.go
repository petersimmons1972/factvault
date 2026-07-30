package research_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petersimmons1972/factvault/internal/collectors"
	"github.com/petersimmons1972/factvault/internal/research"
)

// stubLLMServer returns an httptest.Server that responds to chat completion calls.
// Call 1 returns perspectivesResp; call 2 returns questionsResp.
func stubLLMServer(t *testing.T, perspectivesJSON, questionsJSON string) *httptest.Server {
	t.Helper()
	callN := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callN++
		w.Header().Set("Content-Type", "application/json")
		var content string
		if callN == 1 {
			content = perspectivesJSON
		} else {
			content = questionsJSON
		}
		resp := map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": content}},
			},
		}
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			http.Error(w, encErr.Error(), http.StatusInternalServerError)
		}
	}))
}

// mustJSON marshals v to JSON string, panics on error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func TestGenerateQueries_ColdStart(t *testing.T) {
	perspectivesResp := mustJSON(map[string]any{
		"perspectives": []string{"Historical significance", "Economic impact", "Political context"},
	})
	questionsResp := mustJSON(map[string]any{
		"items": []map[string]any{
			{"perspective": "Historical significance", "queries": []string{"historical significance of Rome", "Rome ancient history timeline"}},
			{"perspective": "Economic impact", "queries": []string{"economic impact Roman Empire", "Rome trade routes"}},
			{"perspective": "Political context", "queries": []string{"Roman Republic politics", "Julius Caesar political role"}},
		},
	})
	srv := stubLLMServer(t, perspectivesResp, questionsResp)
	defer srv.Close()

	cfg := research.Config{
		Perspectives:            5,
		QuestionsPerPerspective: 4,
		ResultsPerQuery:         5,
		MaxTotalFetches:         40,
	}
	entity := research.Entity{Label: "Rome", Type: "City"}
	queries, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queries) == 0 {
		t.Fatal("expected at least one query, got none")
	}
}

func TestGenerateQueries_CapsEnforcedOnOverproduction(t *testing.T) {
	// The (mocked) LLM over-produces at BOTH stages:
	//   - 5 perspectives returned, cap is 2  → only A, B survive
	//   - per perspective: A returns 4 queries, B returns 3, cap is 2 each
	// With caps Perspectives=2, QuestionsPerPerspective=2 the result must be
	// truncated to EXACTLY 4 queries (2 perspectives × 2 questions).
	// All query strings are distinct and share no normalized form, so dedup
	// removes nothing and the count is deterministic.
	perspectivesResp := mustJSON(map[string]any{
		"perspectives": []string{"alpha", "bravo", "charlie", "delta", "echo"}, // over-produces 5, cap=2
	})
	questionsResp := mustJSON(map[string]any{
		"items": []map[string]any{
			{"perspective": "alpha", "queries": []string{"redwood forest height", "kelp tidal zone", "magnetic compass drift", "obsidian volcanic glass"}}, // over-produces 4, cap=2
			{"perspective": "bravo", "queries": []string{"jupiter moon europa", "saharan dust transport", "tungsten melting point"}},                       // over-produces 3, cap=2
			// charlie/delta/echo intentionally absent — perspective cap already dropped them.
		},
	})
	srv := stubLLMServer(t, perspectivesResp, questionsResp)
	defer srv.Close()

	cfg := research.Config{
		Perspectives:            2,
		QuestionsPerPerspective: 2,
		ResultsPerQuery:         5,
		MaxTotalFetches:         40,
	}
	entity := research.Entity{Label: "Test", Type: "Thing"}
	queries, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Exact count: the cap must be REACHED (not under-shot) and not exceeded.
	if len(queries) != 4 {
		t.Errorf("expected exactly 4 queries (2 perspectives × 2 questions, caps reached), got %d: %v", len(queries), queries)
	}
	// And the survivors must be the first-2-of-2 from each surviving perspective.
	want := map[research.Query]bool{
		"redwood forest height":  true,
		"kelp tidal zone":        true,
		"jupiter moon europa":    true,
		"saharan dust transport": true,
	}
	for _, q := range queries {
		if !want[q] {
			t.Errorf("unexpected query %q — expected only the first 2 from each capped perspective", q)
		}
	}
}

func TestGenerateQueries_DedupNormalizedQueries(t *testing.T) {
	perspectivesResp := mustJSON(map[string]any{
		"perspectives": []string{"Science"},
	})
	// "Albert Einstein physics" and "albert  einstein  physics" normalize to same string.
	questionsResp := mustJSON(map[string]any{
		"items": []map[string]any{
			{"perspective": "Science", "queries": []string{
				"Albert Einstein physics",
				"albert  einstein  physics", // duplicate after normalize
				"einstein theory of relativity",
			}},
		},
	})
	srv := stubLLMServer(t, perspectivesResp, questionsResp)
	defer srv.Close()

	cfg := research.Config{
		Perspectives:            3,
		QuestionsPerPerspective: 5,
		ResultsPerQuery:         5,
		MaxTotalFetches:         40,
	}
	entity := research.Entity{Label: "Albert Einstein", Type: "Person"}
	queries, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queries) != 2 {
		t.Errorf("expected 2 deduplicated queries, got %d: %v", len(queries), queries)
	}
}

func TestGenerateQueries_WithGroundingHints(t *testing.T) {
	var capturedBody []byte
	callN := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callN++
		body, _ := io.ReadAll(r.Body) //nolint:errcheck // test stub: error not actionable
		if callN == 1 {
			capturedBody = body
		}
		w.Header().Set("Content-Type", "application/json")
		var content string
		if callN == 1 {
			content = mustJSON(map[string]any{"perspectives": []string{"Health"}})
		} else {
			content = mustJSON(map[string]any{
				"items": []map[string]any{
					{"perspective": "Health", "queries": []string{"aspirin health effects"}},
				},
			})
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		}
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			http.Error(w, encErr.Error(), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := research.Config{
		Perspectives:            3,
		QuestionsPerPerspective: 3,
		ResultsPerQuery:         5,
		MaxTotalFetches:         40,
	}
	entity := research.Entity{Label: "Aspirin", Type: "Drug"}
	hints := []string{"dosage", "side-effects"}
	_, err := research.GenerateQueries(context.Background(), entity, cfg, hints, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bodyStr := string(capturedBody)
	for _, hint := range hints {
		if !strings.Contains(bodyStr, hint) {
			t.Errorf("expected hint %q to appear in first LLM call body", hint)
		}
	}
}

func TestGenerateQueries_ExactlyTwoLLMCalls(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		var content string
		if callCount == 1 {
			content = mustJSON(map[string]any{"perspectives": []string{"A", "B"}})
		} else {
			content = mustJSON(map[string]any{
				"items": []map[string]any{
					{"perspective": "A", "queries": []string{"query about A"}},
					{"perspective": "B", "queries": []string{"query about B"}},
				},
			})
		}
		resp := map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		}
		if encErr := json.NewEncoder(w).Encode(resp); encErr != nil {
			http.Error(w, encErr.Error(), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	cfg := research.Config{
		Perspectives:            5,
		QuestionsPerPerspective: 4,
		ResultsPerQuery:         5,
		MaxTotalFetches:         40,
	}
	entity := research.Entity{Label: "Mars", Type: "Planet"}
	_, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("expected exactly 2 LLM calls, got %d", callCount)
	}
}

func TestGenerateQueries_LLMErrorPropagates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	cfg := research.Config{Perspectives: 1, QuestionsPerPerspective: 1, ResultsPerQuery: 1, MaxTotalFetches: 10}
	entity := research.Entity{Label: "X", Type: "Y"}
	_, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err == nil {
		t.Fatal("expected error from failed LLM call, got nil")
	}
}

func TestQueryNormalization_StopwordStripping(t *testing.T) {
	perspectivesResp := mustJSON(map[string]any{"perspectives": []string{"A"}})
	questionsResp := mustJSON(map[string]any{
		"items": []map[string]any{
			{"perspective": "A", "queries": []string{
				"the quick brown fox",
				"quick brown fox", // same after stripping "the"
			}},
		},
	})
	srv := stubLLMServer(t, perspectivesResp, questionsResp)
	defer srv.Close()

	cfg := research.Config{Perspectives: 1, QuestionsPerPerspective: 5, ResultsPerQuery: 1, MaxTotalFetches: 10}
	entity := research.Entity{Label: "Fox", Type: "Animal"}
	queries, err := research.GenerateQueries(context.Background(), entity, cfg, nil, research.LLMConfig{
		BaseURL: srv.URL,
		APIKey:  "test",
		Model:   "test-model",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(queries) != 1 {
		t.Errorf("expected 1 unique query after stopword dedup, got %d: %v", len(queries), queries)
	}
}

// makeSearchServer returns a test HTTP server that returns a fixed list of URLs for any query.
func makeSearchServer(t *testing.T, urls []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		results := make([]map[string]any, 0, len(urls))
		for _, u := range urls {
			results = append(results, map[string]any{"url": u, "title": "stub"})
		}
		if encErr := json.NewEncoder(w).Encode(map[string]any{"results": results}); encErr != nil {
			http.Error(w, encErr.Error(), http.StatusInternalServerError)
		}
	}))
}

// makeFetchServer returns a test HTTP server that records fetch calls and returns stub HTML.
func makeFetchServer(t *testing.T, rec *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		*rec++
		if _, ferr := fmt.Fprintf(w, "<html><body>stub page %d</body></html>", *rec); ferr != nil {
			http.Error(w, ferr.Error(), http.StatusInternalServerError)
		}
	}))
}

// TestSearchCollector_HardStopAtMaxTotalFetches is the LOAD-BEARING test.
func TestSearchCollector_HardStopAtMaxTotalFetches(t *testing.T) {
	const maxFetches = 3

	fetchCalls := 0
	fetchSrv := makeFetchServer(t, &fetchCalls)
	defer fetchSrv.Close()

	urls := make([]string, 5)
	for i := range urls {
		urls[i] = fmt.Sprintf("%s/page%d", fetchSrv.URL, i)
	}
	searchSrv := makeSearchServer(t, urls)
	defer searchSrv.Close()

	queries := []research.Query{"q1", "q2", "q3", "q4"}

	collector := &research.SearchCollector{
		Queries:         queries,
		ResultsPerQuery: 5,
		MaxTotalFetches: maxFetches,
		SearchURL:       searchSrv.URL,
		HTTPClient:      fetchSrv.Client(),
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fetchCalls != maxFetches {
		t.Errorf("expected exactly %d fetches (MaxTotalFetches), got %d", maxFetches, fetchCalls)
	}
	if len(items) != maxFetches {
		t.Errorf("expected %d items, got %d", maxFetches, len(items))
	}
}

func TestSearchCollector_MetaTrustTierWeb(t *testing.T) {
	fetchCalls := 0
	fetchSrv := makeFetchServer(t, &fetchCalls)
	defer fetchSrv.Close()

	searchSrv := makeSearchServer(t, []string{fetchSrv.URL + "/a"})
	defer searchSrv.Close()

	collector := &research.SearchCollector{
		Queries:         []research.Query{"test query"},
		ResultsPerQuery: 1,
		MaxTotalFetches: 10,
		SearchURL:       searchSrv.URL,
		HTTPClient:      fetchSrv.Client(),
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	meta := items[0].Meta
	if meta == nil {
		t.Fatal("expected Meta to be set, got nil")
	}
	tier, ok := meta["trust_tier"]
	if !ok {
		t.Fatal("expected meta[trust_tier] to be set")
	}
	if tier != "web" {
		t.Errorf("expected trust_tier=web, got %v", tier)
	}
}

func TestSearchCollector_EmptySearchResults(t *testing.T) {
	searchSrv := makeSearchServer(t, nil)
	defer searchSrv.Close()

	collector := &research.SearchCollector{
		Queries:         []research.Query{"no results query"},
		ResultsPerQuery: 5,
		MaxTotalFetches: 10,
		SearchURL:       searchSrv.URL,
		HTTPClient:      http.DefaultClient,
	}

	items, err := collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 items for empty search results, got %d", len(items))
	}
}

func TestSearchCollector_ImplementsCollectorInterface(t *testing.T) {
	// Structural: SearchCollector must implement collectors.Collector.
	var _ collectors.Collector = &research.SearchCollector{}
	collector := &research.SearchCollector{
		Queries:         []research.Query{"x"},
		ResultsPerQuery: 1,
		MaxTotalFetches: 1,
	}
	if collector.Name() == "" {
		t.Error("expected non-empty Name()")
	}
}
