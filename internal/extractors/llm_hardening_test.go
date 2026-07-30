package extractors_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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

// TestLLMExtract_EmptyProposalsArrayIsNotAnError (E-05) verifies that an LLM
// response of {"proposals":[]} is treated as "no facts found" (nil error, empty
// slice) rather than a parse error.  The bug was that the wrapper path fell
// through to the error branch when both Proposals and Statements were empty.
func TestLLMExtract_EmptyProposalsArrayIsNotAnError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]any{"content": `{"proposals":[]}`}},
			},
		}); err != nil {
			t.Errorf("encode: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := extractors.LLMClient{
		BaseURL:    server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	}
	proposals, err := client.Extract(context.Background(), &db.Source{}, "No notable facts here.")
	if err != nil {
		t.Fatalf("E-05: expected nil error for empty proposals, got %v", err)
	}
	if len(proposals) != 0 {
		t.Fatalf("E-05: expected 0 proposals, got %d", len(proposals))
	}
}

// TestLLMExtract_RetriesOn429And500 (E-09) verifies that the LLM client retries
// on HTTP 429 (rate-limit) and >=500 (transient server error) up to 3 attempts
// before succeeding on the third attempt.
func TestLLMExtract_RetriesOn429And500(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := attempts.Add(1)
		switch n {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			return
		case 2:
			w.WriteHeader(http.StatusInternalServerError)
			return
		default:
			// Third attempt: success with a valid proposal.
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": `{"proposals":[{"subject_text":"Acme","property_slug":"org_name","value":"Acme Corp","value_type":"string","excerpt":"Acme","excerpt_offset_start":0,"excerpt_offset_end":4}]}`,
						},
					},
				},
			}); err != nil {
				t.Errorf("encode: %v", err)
			}
		}
	}))
	t.Cleanup(server.Close)

	client := extractors.LLMClient{
		BaseURL:    server.URL,
		Model:      "test-model",
		HTTPClient: server.Client(),
	}
	proposals, err := client.Extract(context.Background(), &db.Source{}, "Acme filed the report.")
	if err != nil {
		t.Fatalf("E-09: expected success after retries, got %v", err)
	}
	if len(proposals) != 1 {
		t.Fatalf("E-09: expected 1 proposal, got %d", len(proposals))
	}
	if attempts.Load() != 3 {
		t.Fatalf("E-09: expected 3 HTTP attempts (429 + 500 + 200), got %d", attempts.Load())
	}
}
