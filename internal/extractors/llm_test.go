package extractors_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

func TestLLMClientExtractParsesStatementProposals(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/chat/completions" {
			t.Fatalf("path = %q, want /chat/completions", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var reqBody map[string]any
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if got := reqBody["model"]; got != "test-model" {
			t.Fatalf("model = %v, want test-model", got)
		}
		responseFormat, ok := reqBody["response_format"].(map[string]any)
		if !ok {
			t.Fatalf("response_format missing or wrong type")
		}
		if got := responseFormat["type"]; got != "json_object" {
			t.Fatalf("response_format.type = %v, want json_object", got)
		}

		proposals := []extractors.StatementProposal{
			{
				SubjectText:        "Acme Co.",
				PropertySlug:       "org_name",
				Value:              "Acme Co.",
				ValueType:          "string",
				Excerpt:            "Acme Co.",
				ExcerptOffsetStart: 0,
				ExcerptOffsetEnd:   8,
			},
		}
		content, err := json.Marshal(map[string]any{"proposals": proposals})
		if err != nil {
			t.Fatalf("marshal proposals: %v", err)
		}
		response := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": string(content),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	client := extractors.LLMClient{
		BaseURL:    server.URL,
		APIKey:     "test-key",
		Model:      "test-model",
		HTTPClient: server.Client(),
	}

	facts, err := client.Extract(context.Background(), &db.Source{
		Title: pgtype.Text{String: "Newswire", Valid: true},
	}, "Acme Co. filed the report.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("Extract() facts = %d, want 1", len(facts))
	}
	if facts[0].PropertySlug != "org_name" {
		t.Fatalf("PropertySlug = %q, want org_name", facts[0].PropertySlug)
	}
}

func TestVerifyExcerptOffsetAndFilterDiscardBadProposal(t *testing.T) {
	t.Parallel()

	rawText := "naïve café"
	good := extractors.StatementProposal{
		SubjectText:        "Cafe",
		PropertySlug:       "name",
		Value:              "café",
		ValueType:          "string",
		Excerpt:            "café",
		ExcerptOffsetStart: 6,
		ExcerptOffsetEnd:   10,
	}
	bad := extractors.StatementProposal{
		SubjectText:        "Cafe",
		PropertySlug:       "name",
		Value:              "café",
		ValueType:          "string",
		Excerpt:            "café",
		ExcerptOffsetStart: 5,
		ExcerptOffsetEnd:   9,
	}

	if !extractors.VerifyExcerptOffset(rawText, good.Excerpt, good.ExcerptOffsetStart, good.ExcerptOffsetEnd) {
		t.Fatal("expected good proposal to verify")
	}
	if extractors.VerifyExcerptOffset(rawText, bad.Excerpt, bad.ExcerptOffsetStart, bad.ExcerptOffsetEnd) {
		t.Fatal("expected bad proposal to fail verification")
	}

	accepted := extractors.FilterVerifiedStatementProposals(rawText, []extractors.StatementProposal{good, bad})
	if len(accepted) != 1 {
		t.Fatalf("accepted = %d, want 1", len(accepted))
	}
	if !bytes.Equal([]byte(accepted[0].Excerpt), []byte("café")) {
		t.Fatalf("accepted excerpt = %q, want café", accepted[0].Excerpt)
	}
}
