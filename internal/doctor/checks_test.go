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
