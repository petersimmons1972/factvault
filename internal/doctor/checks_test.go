package doctor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer server.Close()
	cfg := Config{LLMURL: server.URL, EmbedderURL: server.URL, WaybackURL: server.URL, HTTPClient: server.Client()}
	for _, check := range []struct {
		name string
		fn   func(context.Context, Config) CheckResult
	}{
		{"llm", CheckLLM},
		{"embedder", CheckEmbedder},
		{"wayback", CheckWayback},
	} {
		res := check.fn(context.Background(), cfg)
		if !res.OK {
			t.Fatalf("%s failed: %+v", check.name, res)
		}
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
		{Name: "llm", OK: false, Required: false},       // optional, failing
		{Name: "embedder", OK: false, Required: false},  // optional, failing
		{Name: "wayback", OK: false, Required: false},   // optional, failing
	}
	if !RequiredOK(results) {
		t.Fatal("expected RequiredOK=true when all required checks pass")
	}
}

// TestRequiredOK_FailsOnRequiredFailure verifies that RequiredOK returns false
// when any required check fails.
func TestRequiredOK_FailsOnRequiredFailure(t *testing.T) {
	results := []CheckResult{
		{Name: "postgres", OK: false, Required: true},  // required, failing
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
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
	}))
	defer server.Close()
	cfg := Config{
		LLMURL:      server.URL,
		EmbedderURL: server.URL,
		WaybackURL:  server.URL,
		HTTPClient:  server.Client(),
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
