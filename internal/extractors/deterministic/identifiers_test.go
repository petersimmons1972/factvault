package deterministic_test

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

func TestIdentifierExtractorExtract(t *testing.T) {
	t.Parallel()

	source := &db.Source{
		Title: pgtype.Text{String: "Example filing", Valid: true},
		Url:   "https://example.com",
	}
	rawText := "CIK 0000320193, CUSIP 037833100, ISIN US0378331005, DOI 10.1000/xyz123, NCT01234567, ISBN-13 978-0-306-40615-7"

	var extractor deterministic.IdentifierExtractor
	facts, err := extractor.Extract(context.Background(), source, rawText)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 6 {
		t.Fatalf("Extract() facts = %d, want 6", len(facts))
	}

	got := map[string]map[string]any{}
	for _, fact := range facts {
		got[fact.PropertySlug] = map[string]any{
			"value":   fact.Value,
			"excerpt": fact.Excerpt,
			"start":   fact.ExcerptOffsetStart,
			"end":     fact.ExcerptOffsetEnd,
		}
		if fact.SubjectText != "Example filing" {
			t.Fatalf("SubjectText = %q, want Example filing", fact.SubjectText)
		}
		if fact.ExtractionMethod != "regex:identifiers-v1" {
			t.Fatalf("ExtractionMethod = %q, want regex:identifiers-v1", fact.ExtractionMethod)
		}
	}

	tests := []struct {
		name    string
		slug    string
		value   string
		excerpt string
	}{
		{
			name:    "cik",
			slug:    "sec_cik",
			value:   "0000320193",
			excerpt: "CIK 0000320193",
		},
		{
			name:    "cusip",
			slug:    "sec_cusip",
			value:   "037833100",
			excerpt: "CUSIP 037833100",
		},
		{
			name:    "isin",
			slug:    "sec_isin",
			value:   "US0378331005",
			excerpt: "ISIN US0378331005",
		},
		{
			name:    "doi",
			slug:    "doi",
			value:   "10.1000/xyz123",
			excerpt: "DOI 10.1000/xyz123",
		},
		{
			name:    "nct",
			slug:    "clinicaltrials.gov_nct_id",
			value:   "NCT01234567",
			excerpt: "NCT01234567",
		},
		{
			name:    "isbn13",
			slug:    "isbn_13",
			value:   "9780306406157",
			excerpt: "ISBN-13 978-0-306-40615-7",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fact, ok := got[tt.slug]
			if !ok {
				t.Fatalf("missing fact for %s", tt.slug)
			}
			if fact["value"] != tt.value {
				t.Fatalf("value = %v, want %v", fact["value"], tt.value)
			}
			if fact["excerpt"] != tt.excerpt {
				t.Fatalf("excerpt = %v, want %v", fact["excerpt"], tt.excerpt)
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
