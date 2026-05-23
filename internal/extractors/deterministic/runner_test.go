package deterministic_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

type stubExtractor struct {
	facts []extractors.ExtractedFact
}

func (s stubExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return s.facts, nil
}

func TestRunnerExtractsFromAllDeterministicSources(t *testing.T) {
	t.Parallel()

	source := &db.Source{
		Title: pgtype.Text{String: "Newswire", Valid: true},
	}
	rawText := "Apple Inc. reported $1.2M in revenue on 2024-05-23. Sen. Harris met MSFT on 23 May 2024. CIK 0000320193."

	facts, err := deterministic.NewRunner().Extract(context.Background(), source, rawText)
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}

	required := map[string]bool{
		"sec_cik":     false,
		"usd_amount":  false,
		"date":        false,
		"org_name":    false,
		"person_name": false,
	}
	for _, fact := range facts {
		if _, ok := required[fact.PropertySlug]; ok {
			required[fact.PropertySlug] = true
		}
	}

	for slug, seen := range required {
		if !seen {
			t.Fatalf("missing fact for property slug %q", slug)
		}
	}
}

func TestRunnerDeduplicatesExactDuplicates(t *testing.T) {
	t.Parallel()

	fact := extractors.ExtractedFact{
		SubjectText:        "Newswire",
		PropertySlug:       "org_name",
		Value:              "Apple Inc.",
		ValueType:          "string",
		Excerpt:            "Apple Inc.",
		ExcerptOffsetStart: 0,
		ExcerptOffsetEnd:   10,
		ExtractionMethod:   "gazetteer:v1",
	}

	runner := deterministic.Runner{
		IdentifierExtractor: stubExtractor{facts: []extractors.ExtractedFact{fact}},
		MoneyExtractor:      stubExtractor{facts: []extractors.ExtractedFact{fact}},
		DateExtractor:       stubExtractor{facts: []extractors.ExtractedFact{fact}},
		GazetteerExtractor:  stubExtractor{facts: []extractors.ExtractedFact{fact}},
	}

	facts, err := runner.Extract(context.Background(), &db.Source{}, "Apple Inc.")
	if err != nil {
		t.Fatalf("Extract() error = %v", err)
	}
	if len(facts) != 1 {
		t.Fatalf("Extract() facts = %d, want 1", len(facts))
	}
	if facts[0].Value != "Apple Inc." {
		t.Fatalf("value = %q, want Apple Inc.", facts[0].Value)
	}
}
