package deterministic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

func TestDateExtractorExtract(t *testing.T) {
	t.Parallel()

	source := &db.Source{
		Title: pgtype.Text{String: "Calendar note", Valid: true},
	}
	rawText := "ISO 2024-05-23, month May 23, 2024, day 23 May 2024, timestamp 2024-05-23T12:34:56Z."

	var extractor deterministic.DateExtractor
	facts, err := extractor.Extract(context.Background(), source, rawText)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 4 {
		t.Fatalf("Extract() facts = %d, want 4", len(facts))
	}

	tests := []struct {
		name    string
		excerpt string
		value   string
	}{
		{
			name:    "iso",
			excerpt: "2024-05-23",
			value:   "2024-05-23",
		},
		{
			name:    "month day year",
			excerpt: "May 23, 2024",
			value:   "2024-05-23",
		},
		{
			name:    "day month year",
			excerpt: "23 May 2024",
			value:   "2024-05-23",
		},
		{
			name:    "rfc3339",
			excerpt: "2024-05-23T12:34:56Z",
			value:   "2024-05-23T12:34:56Z",
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
		if fact.PropertySlug != "date" {
			t.Fatalf("PropertySlug = %q, want date", fact.PropertySlug)
		}
		if fact.ValueType != "date" {
			t.Fatalf("ValueType = %q, want date", fact.ValueType)
		}
		if fact.SubjectText != "Calendar note" {
			t.Fatalf("SubjectText = %q, want Calendar note", fact.SubjectText)
		}
		if fact.ExtractionMethod != "regex:dates-v1" {
			t.Fatalf("ExtractionMethod = %q, want regex:dates-v1", fact.ExtractionMethod)
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
