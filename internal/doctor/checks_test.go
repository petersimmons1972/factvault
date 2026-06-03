package doctor

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mockEmbedderServer returns an httptest.Server that satisfies the strengthened
// CheckEmbedder: /health → 200, /info → model+dim, /embed → real-looking
// 1024-dim non-zero vector.
func mockEmbedderServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health", "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"model":"BAAI/bge-m3","dim":1024}`)
		case "/embed":
			// Return a 1024-dim vector with one non-zero component so norm > 0.
			vec := make([]float64, 1024)
			vec[0] = 1.0
			// Build a minimal JSON response.
			w.WriteHeader(http.StatusOK)
			// Emit a compact 1024-element JSON array manually.
			fmt.Fprint(w, `{"vectors":[[1.0`)
			for i := 1; i < 1024; i++ {
				fmt.Fprint(w, `,0.0`)
			}
			fmt.Fprint(w, `]]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
}

func TestHTTPChecks(t *testing.T) {
	// LLM and Wayback only need a simple 200 OK server.
	simpleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer simpleServer.Close()

	embedderServer := mockEmbedderServer(t)
	defer embedderServer.Close()

	for _, check := range []struct {
		name string
		fn   func(context.Context, Config) CheckResult
		url  string
	}{
		{"llm", CheckLLM, simpleServer.URL},
		{"embedder", CheckEmbedder, embedderServer.URL},
		{"wayback", CheckWayback, simpleServer.URL},
	} {
		cfg := Config{
			LLMURL:      check.url,
			EmbedderURL: check.url,
			WaybackURL:  check.url,
			HTTPClient:  simpleServer.Client(),
		}
		if check.name == "embedder" {
			cfg.HTTPClient = embedderServer.Client()
		}
		res := check.fn(context.Background(), cfg)
		if !res.OK {
			t.Fatalf("%s failed: %+v", check.name, res)
		}
	}
}

func TestCheckEmbedder_RejectsZeroVector(t *testing.T) {
	// Ensure CheckEmbedder fails when the embedder returns all-zero vectors (stub behavior).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health", "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/embed":
			w.WriteHeader(http.StatusOK)
			// All-zero 1024-dim vector — the old stub behavior.
			fmt.Fprint(w, `{"vectors":[[0.0`)
			for i := 1; i < 1024; i++ {
				fmt.Fprint(w, `,0.0`)
			}
			fmt.Fprint(w, `]]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
	defer server.Close()

	cfg := Config{EmbedderURL: server.URL, HTTPClient: server.Client()}
	res := CheckEmbedder(context.Background(), cfg)
	if res.OK {
		t.Fatal("expected CheckEmbedder to fail on zero-vector stub; got OK=true")
	}
}

func TestCheckEmbedder_RejectsWrongDimension(t *testing.T) {
	// Ensure CheckEmbedder fails when the embedder returns the wrong dimension.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health", "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/embed":
			// 384-dim non-zero vector (wrong model).
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"vectors":[[1.0`)
			for i := 1; i < 384; i++ {
				fmt.Fprint(w, `,0.0`)
			}
			fmt.Fprint(w, `]]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
	defer server.Close()

	cfg := Config{EmbedderURL: server.URL, HTTPClient: server.Client()}
	res := CheckEmbedder(context.Background(), cfg)
	if res.OK {
		t.Fatalf("expected CheckEmbedder to fail on 384-dim vector; got OK=true, detail=%q", res.Detail)
	}
}

func TestAllOK(t *testing.T) {
	if !AllOK([]CheckResult{{OK: true}, {OK: true}}) {
		t.Fatal("expected all ok")
	}
	if AllOK([]CheckResult{{OK: true}, {OK: false}}) {
		t.Fatal("expected not ok")
	}
}

// TestRequiredOK_IgnoresOptionalFailures verifies that RequiredOK returns true
// when all required checks pass even if optional checks fail.
func TestRequiredOK_IgnoresOptionalFailures(t *testing.T) {
	results := []CheckResult{
		{Name: "postgres", OK: true, Required: true},
		{Name: "migrations", OK: true, Required: true},
		{Name: "rls", OK: true, Required: true},
		{Name: "canary", OK: true, Required: true},
		{Name: "llm", OK: false, Required: false},      // optional, failing
		{Name: "embedder", OK: false, Required: false}, // optional, failing
		{Name: "wayback", OK: false, Required: false},  // optional, failing
	}
	if !RequiredOK(results) {
		t.Fatal("expected RequiredOK=true when all required checks pass")
	}
}

// TestRequiredOK_FailsOnRequiredFailure verifies that RequiredOK returns false
// when any required check fails.
func TestRequiredOK_FailsOnRequiredFailure(t *testing.T) {
	results := []CheckResult{
		{Name: "postgres", OK: false, Required: true}, // required, failing
		{Name: "migrations", OK: true, Required: true},
		{Name: "rls", OK: true, Required: true},
		{Name: "canary", OK: true, Required: true},
		{Name: "llm", OK: true, Required: false},
	}
	if RequiredOK(results) {
		t.Fatal("expected RequiredOK=false when a required check fails")
	}
}

// TestRunAll_SetsRequiredField verifies that RunAll sets Required=true on
// required checks and Required=false on optional checks. Uses an httptest
// server so optional HTTP checks pass.
func TestRunAll_SetsRequiredField(t *testing.T) {
	// LLM and Wayback just need 200 OK; embedder needs the full mock.
	embedderServer := mockEmbedderServer(t)
	defer embedderServer.Close()

	simpleServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"object":"list","data":[]}`)
	}))
	defer simpleServer.Close()

	cfg := Config{
		LLMURL:      simpleServer.URL,
		EmbedderURL: embedderServer.URL,
		WaybackURL:  simpleServer.URL,
		HTTPClient:  embedderServer.Client(),
	}
	results := RunAll(context.Background(), cfg)
	for _, r := range results {
		switch r.Name {
		case "postgres", "migrations", "rls", "canary":
			if !r.Required {
				t.Errorf("check %q: want Required=true, got false", r.Name)
			}
		case "llm", "embedder", "wayback":
			if r.Required {
				t.Errorf("check %q: want Required=false, got true", r.Name)
			}
		}
	}
}

// TestNormThresholdFor verifies that BGE/E5 model names get the tight threshold
// and unknown models get the loose (non-zero-only) threshold.
func TestNormThresholdFor(t *testing.T) {
	cases := []struct {
		model         string
		wantThreshold float64
		wantLabel     string
	}{
		{"BAAI/bge-m3", 0.5, "BGE/E5 normalised"},
		{"bge-large-en", 0.5, "BGE/E5 normalised"},
		{"intfloat/e5-large-v2", 0.5, "BGE/E5 normalised"},
		{"E5-small", 0.5, "BGE/E5 normalised"},
		{"sentence-transformers/all-MiniLM-L6-v2", 1e-9, "non-zero"},
		{"nomic-embed-text", 1e-9, "non-zero"},
		{"", 1e-9, "non-zero"},
	}
	for _, tc := range cases {
		thresh, label := normThresholdFor(tc.model)
		if thresh != tc.wantThreshold {
			t.Errorf("normThresholdFor(%q): threshold=%.6g, want %.6g", tc.model, thresh, tc.wantThreshold)
		}
		if label != tc.wantLabel {
			t.Errorf("normThresholdFor(%q): label=%q, want %q", tc.model, label, tc.wantLabel)
		}
	}
}

// TestCheckEmbedder_NonBGEModelPassesWithLowNorm verifies that a non-BGE model
// returning a low-but-nonzero 1024-dim vector is accepted (loose threshold).
func TestCheckEmbedder_NonBGEModelPassesWithLowNorm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health", "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/info":
			// Non-BGE, non-E5 model name.
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"model":"nomic-embed-text","dim":1024}`)
		case "/embed":
			// Very small but non-zero norm — fine for non-normalised models.
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"vectors":[[0.000001`)
			for i := 1; i < 1024; i++ {
				fmt.Fprint(w, `,0.0`)
			}
			fmt.Fprint(w, `]]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
	defer server.Close()

	cfg := Config{EmbedderURL: server.URL, HTTPClient: server.Client()}
	res := CheckEmbedder(context.Background(), cfg)
	if !res.OK {
		t.Fatalf("expected CheckEmbedder to pass for non-BGE model with low-but-nonzero norm; got OK=false, detail=%q", res.Detail)
	}
}

// TestCheckEmbedder_BGEModelFailsWithLowNorm verifies that a BGE model returning
// a near-zero vector (norm=0.01, below the 0.5 threshold) is rejected.
func TestCheckEmbedder_BGEModelFailsWithLowNorm(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/health", "/healthz":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		case "/info":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"model":"BAAI/bge-m3","dim":1024}`)
		case "/embed":
			// norm = 0.01 — non-zero but well below the BGE threshold of 0.5.
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"vectors":[[0.01`)
			for i := 1; i < 1024; i++ {
				fmt.Fprint(w, `,0.0`)
			}
			fmt.Fprint(w, `]]}`)
		default:
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		}
	}))
	defer server.Close()

	cfg := Config{EmbedderURL: server.URL, HTTPClient: server.Client()}
	res := CheckEmbedder(context.Background(), cfg)
	if res.OK {
		t.Fatalf("expected CheckEmbedder to fail for BGE model with norm=0.01; got OK=true, detail=%q", res.Detail)
	}
}
