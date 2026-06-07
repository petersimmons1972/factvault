// Package deterministic implements deterministic extraction logic for factual values.
package deterministic

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

// GazetteerExtractor extracts common named-entity facts from predefined lists.
type GazetteerExtractor struct{}

type gazetteerEntry struct {
	Canonical    string
	Aliases      []string
	PropertySlug string
	ValueType    string
}

var defaultGazetteerEntries = []gazetteerEntry{
	{
		Canonical:    "Apple Inc.",
		Aliases:      []string{"Apple"},
		PropertySlug: "org_name",
		ValueType:    "string",
	},
	{
		Canonical:    "Microsoft Corporation",
		Aliases:      []string{"Microsoft", "MSFT"},
		PropertySlug: "org_name",
		ValueType:    "string",
	},
	{
		Canonical:    "Kamala Harris",
		Aliases:      []string{"Sen. Harris", "Vice President Harris"},
		PropertySlug: "person_name",
		ValueType:    "string",
	},
	{
		Canonical:    "Joe Biden",
		Aliases:      []string{"President Biden", "Biden"},
		PropertySlug: "person_name",
		ValueType:    "string",
	},
}

// Extract returns dictionary-backed facts when alias matches are found.
func (GazetteerExtractor) Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	_ = ctx

	subject := sourceLabel(source)
	facts := make([]extractors.ExtractedFact, 0)
	for _, entry := range defaultGazetteerEntries {
		facts = appendGazetteerFacts(facts, subject, rawText, entry)
	}
	return facts, nil
}

func appendGazetteerFacts(
	facts []extractors.ExtractedFact,
	subject string,
	rawText string,
	entry gazetteerEntry,
) []extractors.ExtractedFact {
	phrases := append([]string{entry.Canonical}, entry.Aliases...)
	for _, phrase := range phrases {
		lowerPhrase := strings.ToLower(phrase)
		searchFrom := 0
		for {
			idx := strings.Index(strings.ToLower(rawText[searchFrom:]), lowerPhrase)
			if idx < 0 {
				break
			}
			start := searchFrom + idx
			end := start + len(phrase)
			if hasWordBoundaries(rawText, start, end) && !gazetteerFactOverlaps(facts, start, end) {
				facts = append(facts, extractors.ExtractedFact{
					SubjectText:        subject,
					PropertySlug:       entry.PropertySlug,
					Value:              entry.Canonical,
					ValueType:          entry.ValueType,
					Excerpt:            rawText[start:end],
					ExcerptOffsetStart: byteOffsetToCharOffset(rawText, start),
					ExcerptOffsetEnd:   byteOffsetToCharOffset(rawText, end),
					ExtractionMethod:   "gazetteer:v1",
				})
			}
			searchFrom = end
		}
	}
	return facts
}

func gazetteerFactOverlaps(facts []extractors.ExtractedFact, start, end int) bool {
	for _, fact := range facts {
		existingStart := fact.ExcerptOffsetStart
		existingEnd := fact.ExcerptOffsetEnd
		if start < existingEnd && end > existingStart {
			return true
		}
	}
	return false
}

func hasWordBoundaries(rawText string, start, end int) bool {
	if start > 0 {
		prev, _ := utf8.DecodeLastRuneInString(rawText[:start])
		if unicode.IsLetter(prev) || unicode.IsDigit(prev) {
			return false
		}
	}
	if end < len(rawText) {
		next, _ := utf8.DecodeRuneInString(rawText[end:])
		if unicode.IsLetter(next) || unicode.IsDigit(next) {
			return false
		}
	}
	return true
}
