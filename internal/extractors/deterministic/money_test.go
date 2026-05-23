package deterministic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

func TestMoneyExtractorExtract(t *testing.T) {
	t.Parallel()

	source := &db.Source{
		Title: pgtype.Text{String: "Quarterly report", Valid: true},
	}
	rawText := "Revenue was $1.2M, guidance US$2,500, and debt USD 3 billion. Ignore €4m."

	var extractor deterministic.MoneyExtractor
	facts, err := extractor.Extract(context.Background(), source, rawText)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("Extract() facts = %d, want 3", len(facts))
	}

	tests := []struct {
		name    string
		value   string
		excerpt string
	}{
		{
			name:    "suffix million",
			value:   "1200000",
			excerpt: "$1.2M",
		},
		{
			name:    "plain usd",
			value:   "2500",
			excerpt: "US$2,500",
		},
		{
			name:    "word suffix billion",
			value:   "3000000000",
			excerpt: "USD 3 billion",
		},
	}

	got := map[string]map[string]any{}
	for _, fact := range facts {
		got[fact.Excerpt] = map[string]any{
			"value":  fact.Value,
			"start":  fact.ExcerptOffsetStart,
			"end":    fact.ExcerptOffsetEnd,
			"method": fact.ExtractionMethod,
		}
		if fact.PropertySlug != "usd_amount" {
			t.Fatalf("PropertySlug = %q, want usd_amount", fact.PropertySlug)
		}
		if fact.ValueType != "number" {
			t.Fatalf("ValueType = %q, want number", fact.ValueType)
		}
		if fact.SubjectText != "Quarterly report" {
			t.Fatalf("SubjectText = %q, want Quarterly report", fact.SubjectText)
		}
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fact, ok := got[tt.excerpt]
			if !ok {
				t.Fatalf("missing fact for excerpt %q", tt.excerpt)
			}
			if fact["value"] != tt.value {
				t.Fatalf("value = %v, want %v", fact["value"], tt.value)
			}
			if fact["method"] != "regex:money-v1" {
				t.Fatalf("method = %v, want regex:money-v1", fact["method"])
			}

			wantStart := strings.Index(rawText, tt.excerpt)
			wantEnd := wantStart + len(tt.excerpt)
			if fact["start"] != wantStart {
				t.Fatalf("start = %v, want %v", fact["start"], wantStart)
			}
			if fact["end"] != wantEnd {
				t.Fatalf("end = %v, want %v", fact["end"], wantEnd)
			}
		})
	}
}
