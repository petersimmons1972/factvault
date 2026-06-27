package deterministic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

// TestGazetteerExtractor_MultibyteUTF8OffsetIsRuneNotByte (E-07) verifies that
// ExcerptOffsetStart / ExcerptOffsetEnd are rune (character) indices rather than
// byte indices.  Before the fix the gazetteer comparison used raw byte positions
// against stored rune positions causing false non-overlaps on multi-byte text.
func TestGazetteerExtractor_MultibyteUTF8OffsetIsRuneNotByte(t *testing.T) {
	// "αβ " = α(2 bytes)+β(2 bytes)+space(1 byte) = 5 bytes, 3 rune chars.
	// "Apple" starts at byte 5 / rune 3 and ends at byte 10 / rune 8.
	rawText := "αβ Apple released a product."
	var ex deterministic.GazetteerExtractor
	facts, err := ex.Extract(context.Background(), &db.Source{}, rawText)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var found bool
	for _, f := range facts {
		if f.Excerpt == "Apple" {
			found = true
			// Rune offsets: α=1, β=1, ' '=1 → start=3; "Apple"=5 runes → end=8
			if f.ExcerptOffsetStart != 3 {
				t.Errorf("E-07: ExcerptOffsetStart = %d, want 3 (rune offset); byte offset would be 5", f.ExcerptOffsetStart)
			}
			if f.ExcerptOffsetEnd != 8 {
				t.Errorf("E-07: ExcerptOffsetEnd = %d, want 8 (rune offset); byte offset would be 10", f.ExcerptOffsetEnd)
			}
		}
	}
	if !found {
		t.Fatal("expected gazetteer fact for 'Apple' not found")
	}
}

func TestGazetteerExtractorExtract(t *testing.T) {
	t.Parallel()

	source := &db.Source{
		Title: pgtype.Text{String: "Newswire", Valid: true},
	}
	rawText := "apple inc. met with Sen. Harris and MSFT in Seattle."

	var extractor deterministic.GazetteerExtractor
	facts, err := extractor.Extract(context.Background(), source, rawText)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 3 {
		t.Fatalf("Extract() facts = %d, want 3", len(facts))
	}

	tests := []struct {
		name    string
		excerpt string
		value   string
		slug    string
	}{
		{
			name:    "apple",
			excerpt: "apple inc.",
			value:   "Apple Inc.",
			slug:    "org_name",
		},
		{
			name:    "harris",
			excerpt: "Sen. Harris",
			value:   "Kamala Harris",
			slug:    "person_name",
		},
		{
			name:    "msft",
			excerpt: "MSFT",
			value:   "Microsoft Corporation",
			slug:    "org_name",
		},
	}

	got := map[string]map[string]any{}
	for _, fact := range facts {
		got[fact.Excerpt] = map[string]any{
			"value":  fact.Value,
			"start":  fact.ExcerptOffsetStart,
			"end":    fact.ExcerptOffsetEnd,
			"method": fact.ExtractionMethod,
			"slug":   fact.PropertySlug,
		}
		if fact.SubjectText != "Newswire" {
			t.Fatalf("SubjectText = %q, want Newswire", fact.SubjectText)
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
			if fact["slug"] != tt.slug {
				t.Fatalf("slug = %v, want %v", fact["slug"], tt.slug)
			}
			if fact["method"] != "gazetteer:v1" {
				t.Fatalf("method = %v, want gazetteer:v1", fact["method"])
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
