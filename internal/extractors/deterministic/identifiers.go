package deterministic

import (
	"context"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

type IdentifierExtractor struct{}

var (
	cikPattern    = regexp.MustCompile(`(?i)\b(?:CIK\s*)?(\d{10})\b`)
	cusipPattern  = regexp.MustCompile(`(?i)\bCUSIP[:\s]*([A-Z0-9]{9})\b`)
	isinPattern   = regexp.MustCompile(`(?i)\bISIN[:\s]*([A-Z]{2}[A-Z0-9]{9}\d)\b`)
	doiPattern    = regexp.MustCompile(`(?i)\b(?:DOI[:\s]*)?(10\.\d{4,9}/[-._;()/:A-Z0-9]+)\b`)
	nctPattern    = regexp.MustCompile(`(?i)\b(NCT\d{8})\b`)
	isbn13Pattern = regexp.MustCompile(`(?i)\b(?:ISBN(?:-13)?[:\s]*)?((?:97[89])(?:[-\s]?\d){10})\b`)
)

func (IdentifierExtractor) Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	_ = ctx

	subject := sourceLabel(source)
	facts := make([]extractors.ExtractedFact, 0)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "sec_cik", "string", "regex:identifiers-v1", cikPattern, normalizeDigits)...)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "sec_cusip", "string", "regex:identifiers-v1", cusipPattern, normalizeUpper)...)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "sec_isin", "string", "regex:identifiers-v1", isinPattern, normalizeUpper)...)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "doi", "string", "regex:identifiers-v1", doiPattern, normalizeLower)...)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "clinicaltrials.gov_nct_id", "string", "regex:identifiers-v1", nctPattern, normalizeUpper)...)
	facts = append(facts, collectIdentifierFacts(subject, rawText, "isbn_13", "string", "regex:identifiers-v1", isbn13Pattern, normalizeISBN13)...)
	return facts, nil
}

func collectIdentifierFacts(
	subject string,
	rawText string,
	propertySlug string,
	valueType string,
	method string,
	pattern *regexp.Regexp,
	normalize func(string) string,
) []extractors.ExtractedFact {
	matches := pattern.FindAllStringSubmatchIndex(rawText, -1)
	facts := make([]extractors.ExtractedFact, 0, len(matches))
	for _, match := range matches {
		if len(match) < 4 {
			continue
		}
		excerptStart, excerptEnd := match[0], match[1]
		valueStart, valueEnd := match[2], match[3]
		excerpt := rawText[excerptStart:excerptEnd]
		value := rawText[valueStart:valueEnd]
		if normalize != nil {
			value = normalize(value)
		}
		facts = append(facts, extractors.ExtractedFact{
			SubjectText:        subject,
			PropertySlug:       propertySlug,
			Value:              value,
			ValueType:          valueType,
			Excerpt:            excerpt,
			ExcerptOffsetStart: byteOffsetToCharOffset(rawText, excerptStart),
			ExcerptOffsetEnd:   byteOffsetToCharOffset(rawText, excerptEnd),
			ExtractionMethod:   method,
		})
	}
	return facts
}

func sourceLabel(source *db.Source) string {
	if source == nil {
		return ""
	}
	if title, ok := extractors.TextValue(source.Title); ok {
		return title
	}
	return source.Url
}

func byteOffsetToCharOffset(s string, offset int) int {
	if offset <= 0 {
		return 0
	}
	if offset >= len(s) {
		return utf8.RuneCountInString(s)
	}
	return utf8.RuneCountInString(s[:offset])
}

func normalizeDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeUpper(s string) string {
	return strings.ToUpper(strings.TrimSpace(s))
}

func normalizeLower(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func normalizeISBN13(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}
