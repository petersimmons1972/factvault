package extractors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

func TestLLMBodyLimitEnforced(t *testing.T) {
	largeProposal := map[string]any{
		"proposals": []map[string]any{
			{
				"subject_text":         "Acme Co.",
				"property_slug":        "org_name",
				"value":                strings.Repeat("v", 4*1024*1024),
				"value_type":           "string",
				"excerpt":              "Acme Co.",
				"excerpt_offset_start": 0,
				"excerpt_offset_end":   8,
			},
		},
	}
	content, err := json.Marshal(largeProposal)
	if err != nil {
		t.Fatalf("marshal proposal: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": string(content),
					},
				},
			},
		}); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := extractors.LLMClient{
		BaseURL:    server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	}

	if _, err := client.Extract(context.Background(), &db.Source{}, "Acme Co. filed the report."); err == nil {
		t.Fatal("expected oversized LLM response body to fail")
	}
}
