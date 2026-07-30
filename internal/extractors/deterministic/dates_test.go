package deterministic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

// TestDateExtractorExtract_MultibyteUTF8OffsetIsRuneNotByte (E-06) verifies that
// ExcerptOffsetStart and ExcerptOffsetEnd are stored as rune (character) offsets
// rather than byte offsets.  Before the fix, offsets were byte indices and would
// be wrong for any text containing multi-byte UTF-8 code points.
func TestDateExtractorExtract_MultibyteUTF8OffsetIsRuneNotByte(t *testing.T) {
	// "αβ " = α(2 bytes)+β(2 bytes)+space(1 byte) = 5 bytes, 3 rune chars.
	// The ISO date "2024-05-23" starts at byte 5 / rune 3.
	// The date is 10 ASCII chars → ends at byte 15 / rune 13.
	rawText := "αβ 2024-05-23 is the date"
	var ex deterministic.DateExtractor
	facts, err := ex.Extract(context.Background(), &db.Source{}, rawText)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	var found bool
	for _, f := range facts {
		if f.Excerpt == "2024-05-23" {
			found = true
			// Rune offset: α=1, β=1, ' '=1 → start=3; end=3+10=13
			if f.ExcerptOffsetStart != 3 {
				t.Errorf("E-06: ExcerptOffsetStart = %d, want 3 (rune offset); byte offset would be 5", f.ExcerptOffsetStart)
			}
			if f.ExcerptOffsetEnd != 13 {
				t.Errorf("E-06: ExcerptOffsetEnd = %d, want 13 (rune offset); byte offset would be 15", f.ExcerptOffsetEnd)
			}
		}
	}
	if !found {
		t.Fatal("expected date fact for 2024-05-23 not found")
	}
}

// TestDateExtractorExtract_MultibyteOverlapDetection (E-06) verifies that when a
// RFC3339 timestamp is extracted first, the ISO date within it is correctly
// identified as overlapping and NOT emitted as a second fact, even when there are
// multi-byte chars earlier in the text (causing byte offset ≠ rune offset).
func TestDateExtractorExtract_MultibyteOverlapDetection(t *testing.T) {
	// "αβγ " = 6 bytes, 4 rune chars.  The RFC3339 date is extracted first;
	// ISO pattern should detect overlap and not emit a duplicate.
	rawText := "αβγ 2024-01-15T10:00:00Z end"
	var ex deterministic.DateExtractor
	facts, err := ex.Extract(context.Background(), &db.Source{}, rawText)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Should have exactly 1 date fact (the RFC3339); the ISO date "2024-01-15"
	// is a strict subset and must be detected as overlapping.
	var rfc3339Count, isoCount int
	for _, f := range facts {
		if strings.HasPrefix(f.Value, "2024-01-15T") {
			rfc3339Count++
		}
		if f.Value == "2024-01-15" {
			isoCount++
		}
	}
	if rfc3339Count != 1 {
		t.Errorf("E-06: expected 1 RFC3339 fact, got %d", rfc3339Count)
	}
	if isoCount != 0 {
		t.Errorf("E-06: expected 0 duplicate ISO date facts (overlap not detected), got %d; byte/rune mismatch may be the cause", isoCount)
	}
}

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
