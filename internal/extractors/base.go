package extractors

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/petersimmons1972/factvault/internal/db"
)

// ExtractedFact is the canonical fact record emitted by deterministic and LLM extractors.
type ExtractedFact struct {
	SubjectText        string
	PropertySlug       string
	Value              string
	ValueType          string
	Excerpt            string
	ExcerptOffsetStart int
	ExcerptOffsetEnd   int
	ExtractionMethod   string
	SourceConfidence   *float64
}

// Extractor emits normalized facts for a single source record.
type Extractor interface {
	Extract(ctx context.Context, source *db.Source, rawText string) ([]ExtractedFact, error)
}

// SourceRawText returns the archived raw text for a source when it is present.
func SourceRawText(source *db.Source) (string, bool) {
	if source == nil {
		return "", false
	}
	return TextValue(source.RawText)
}

// TextValue returns the string content of a nullable pgtype.Text when present.
func TextValue(value pgtype.Text) (string, bool) {
	if !value.Valid {
		return "", false
	}
	return value.String, true
}
