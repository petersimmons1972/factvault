// Package deterministic implements deterministic extraction logic for factual values.
package deterministic

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

// DateExtractor extracts date facts from text.
type DateExtractor struct{}

var (
	rfc3339DatePattern  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:Z|[+-]\d{2}:\d{2})\b`)
	isoDatePattern      = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	monthDayYearPattern = regexp.MustCompile(`\b(?:January|February|March|April|May|June|July|August|September|October|November|December) \d{1,2}, \d{4}\b`)
	dayMonthYearPattern = regexp.MustCompile(`\b\d{1,2} (?:January|February|March|April|May|June|July|August|September|October|November|December) \d{4}\b`)
)

// Extract returns date facts discovered in raw text using deterministic regex patterns.
func (DateExtractor) Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	_ = ctx

	subject := sourceLabel(source)
	facts := make([]extractors.ExtractedFact, 0)
	facts = appendDateFacts(facts, subject, rawText, "regex:dates-v1", rfc3339DatePattern, normalizeRFC3339Date)
	facts = appendDateFacts(facts, subject, rawText, "regex:dates-v1", monthDayYearPattern, normalizeMonthDayYearDate)
	facts = appendDateFacts(facts, subject, rawText, "regex:dates-v1", dayMonthYearPattern, normalizeDayMonthYearDate)
	facts = appendDateFacts(facts, subject, rawText, "regex:dates-v1", isoDatePattern, normalizeISODate)
	return facts, nil
}

func appendDateFacts(
	facts []extractors.ExtractedFact,
	subject string,
	rawText string,
	method string,
	pattern *regexp.Regexp,
	normalize func(string) (string, bool),
) []extractors.ExtractedFact {
	matches := pattern.FindAllStringIndex(rawText, -1)
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		start, end := match[0], match[1]
		excerpt := rawText[start:end]
		value, ok := normalize(excerpt)
		if !ok {
			continue
		}
		if overlapsExistingDateFact(rawText, facts, start, end) {
			continue
		}
		facts = append(facts, extractors.ExtractedFact{
			SubjectText:        subject,
			PropertySlug:       "date",
			Value:              value,
			ValueType:          "date",
			Excerpt:            excerpt,
			ExcerptOffsetStart: byteOffsetToCharOffset(rawText, start),
			ExcerptOffsetEnd:   byteOffsetToCharOffset(rawText, end),
			ExtractionMethod:   method,
		})
	}
	return facts
}

// overlapsExistingDateFact reports whether the byte range [start, end) in
// rawText overlaps any already-extracted fact. The stored ExcerptOffsetStart
// and ExcerptOffsetEnd are rune (character) offsets, so the incoming byte
// offsets must be converted first (E-06: mismatch caused false non-overlaps on
// multi-byte UTF-8 text).
func overlapsExistingDateFact(rawText string, facts []extractors.ExtractedFact, start, end int) bool {
	runeStart := byteOffsetToCharOffset(rawText, start)
	runeEnd := byteOffsetToCharOffset(rawText, end)
	for _, fact := range facts {
		if runeStart < fact.ExcerptOffsetEnd && runeEnd > fact.ExcerptOffsetStart {
			return true
		}
	}
	return false
}

func normalizeRFC3339Date(text string) (string, bool) {
	t, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return "", false
	}
	return t.UTC().Format(time.RFC3339), true
}

func normalizeISODate(text string) (string, bool) {
	t, err := time.Parse("2006-01-02", text)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

func normalizeMonthDayYearDate(text string) (string, bool) {
	t, err := time.Parse("January 2, 2006", text)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}

func normalizeDayMonthYearDate(text string) (string, bool) {
	if strings.Count(text, ",") != 0 {
		return "", false
	}
	t, err := time.Parse("2 January 2006", text)
	if err != nil {
		return "", false
	}
	return t.Format("2006-01-02"), true
}
