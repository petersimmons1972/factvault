// Package deterministic implements deterministic extraction logic for factual values.
package deterministic

import (
	"context"
	"fmt"
	"strings"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

// Runner coordinates deterministic extractors over deterministic inputs.
type Runner struct {
	IdentifierExtractor extractors.Extractor
	MoneyExtractor      extractors.Extractor
	DateExtractor       extractors.Extractor
	GazetteerExtractor  extractors.Extractor
}

// NewRunner constructs a Runner with the built-in extractor implementations.
func NewRunner() Runner {
	return Runner{
		IdentifierExtractor: IdentifierExtractor{},
		MoneyExtractor:      MoneyExtractor{},
		DateExtractor:       DateExtractor{},
		GazetteerExtractor:  GazetteerExtractor{},
	}
}

// Extract runs each configured extractor and deduplicates extracted facts.
func (r Runner) Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	if r.IdentifierExtractor == nil || r.MoneyExtractor == nil || r.DateExtractor == nil || r.GazetteerExtractor == nil {
		defaultRunner := NewRunner()
		if r.IdentifierExtractor == nil {
			r.IdentifierExtractor = defaultRunner.IdentifierExtractor
		}
		if r.MoneyExtractor == nil {
			r.MoneyExtractor = defaultRunner.MoneyExtractor
		}
		if r.DateExtractor == nil {
			r.DateExtractor = defaultRunner.DateExtractor
		}
		if r.GazetteerExtractor == nil {
			r.GazetteerExtractor = defaultRunner.GazetteerExtractor
		}
	}

	var out []extractors.ExtractedFact
	seen := map[string]struct{}{}
	pipeline := []extractors.Extractor{
		r.IdentifierExtractor,
		r.MoneyExtractor,
		r.DateExtractor,
		r.GazetteerExtractor,
	}
	for _, ex := range pipeline {
		facts, err := ex.Extract(ctx, source, rawText)
		if err != nil {
			return nil, fmt.Errorf("%T: %w", ex, err)
		}
		for _, fact := range facts {
			key := factKey(fact)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, fact)
		}
	}
	return out, nil
}

func factKey(fact extractors.ExtractedFact) string {
	parts := []string{
		fact.SubjectText,
		fact.PropertySlug,
		fact.Value,
		fact.ValueType,
		fact.Excerpt,
		fmt.Sprintf("%d", fact.ExcerptOffsetStart),
		fmt.Sprintf("%d", fact.ExcerptOffsetEnd),
		fact.ExtractionMethod,
	}
	return strings.Join(parts, "\x1f")
}
