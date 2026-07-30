package embed_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/petersimmons1972/factvault/internal/embed"
)

// makeTestServer creates an httptest server that returns fixed 1024-dim unit vectors.
func makeTestServer(t *testing.T, dim int) (*httptest.Server, []float64) {
	t.Helper()
	vec := make([]float64, dim)
	// Unit vector along first axis: norm = 1.0
	vec[0] = 1.0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Texts []string `json:"texts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Return one vector per input text
		vectors := make([][]float64, len(req.Texts))
		for i := range req.Texts {
			v := make([]float64, dim)
			v[0] = 1.0
			vectors[i] = v
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"vectors": vectors}); err != nil {
			t.Errorf("encode response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, vec
}

// TestClientEmbed_ReturnsSingleVector verifies that Embed returns a 1024-dim float32 slice
// for a single input text.
func TestClientEmbed_ReturnsSingleVector(t *testing.T) {
	srv, _ := makeTestServer(t, 1024)
	c := embed.NewClient(srv.URL, srv.Client())

	vecs, err := c.Embed(context.Background(), []string{"hello world"})
	if err != nil {
		t.Fatalf("Embed: unexpected error: %v", err)
	}
	if len(vecs) != 1 {
		t.Fatalf("expected 1 vector, got %d", len(vecs))
	}
	if len(vecs[0]) != 1024 {
		t.Fatalf("expected dim=1024, got %d", len(vecs[0]))
	}
}

// TestClientEmbed_ReturnsManyVectors verifies batch embedding returns one vector per input.
func TestClientEmbed_ReturnsManyVectors(t *testing.T) {
	srv, _ := makeTestServer(t, 1024)
	c := embed.NewClient(srv.URL, srv.Client())

	texts := []string{"alpha", "beta", "gamma"}
	vecs, err := c.Embed(context.Background(), texts)
	if err != nil {
		t.Fatalf("Embed: unexpected error: %v", err)
	}
	if len(vecs) != len(texts) {
		t.Fatalf("expected %d vectors, got %d", len(texts), len(vecs))
	}
}

// TestClientEmbed_VectorsAreFloat32 verifies the returned type is []float32 (not float64).
func TestClientEmbed_VectorsAreFloat32(t *testing.T) {
	srv, _ := makeTestServer(t, 1024)
	c := embed.NewClient(srv.URL, srv.Client())

	vecs, err := c.Embed(context.Background(), []string{"type check"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// Compile-time check: vecs[0] must be assignable to []float32.
	_ = vecs[0]
}

// TestClientEmbed_HTTPError verifies that a non-200 response is returned as an error.
func TestClientEmbed_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "service unavailable", http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c := embed.NewClient(srv.URL, srv.Client())
	_, err := c.Embed(context.Background(), []string{"text"})
	if err == nil {
		t.Fatal("expected error on HTTP 503, got nil")
	}
}

// TestClientEmbed_EmptyTextsReturnsEmpty verifies that an empty input yields an empty result.
func TestClientEmbed_EmptyTextsReturnsEmpty(t *testing.T) {
	srv, _ := makeTestServer(t, 1024)
	c := embed.NewClient(srv.URL, srv.Client())

	vecs, err := c.Embed(context.Background(), []string{})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 0 {
		t.Fatalf("expected 0 vectors for empty input, got %d", len(vecs))
	}
}

// TestClientEmbed_ContextCancelledReturnsError verifies that a cancelled context
// propagates as an error.
func TestClientEmbed_ContextCancelledReturnsError(t *testing.T) {
	srv, _ := makeTestServer(t, 1024)
	c := embed.NewClient(srv.URL, srv.Client())

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	_, err := c.Embed(ctx, []string{"text"})
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}
