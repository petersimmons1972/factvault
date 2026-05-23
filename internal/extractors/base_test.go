package extractors_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
)

type stubExtractor struct{}

func (stubExtractor) Extract(context.Context, *db.Source, string) ([]extractors.ExtractedFact, error) {
	return []extractors.ExtractedFact{}, nil
}

func TestExtractedFactZeroValueAndEquality(t *testing.T) {
	t.Parallel()

	confidence := 0.9
	a := extractors.ExtractedFact{
		SubjectText:        "Apple Inc.",
		PropertySlug:       "sec_cik",
		Value:              "0000320193",
		ValueType:          "string",
		Excerpt:            "Apple Inc. (CIK 0000320193)",
		ExcerptOffsetStart: 0,
		ExcerptOffsetEnd:   26,
		ExtractionMethod:   "regex:identifiers-v1",
		SourceConfidence:   &confidence,
	}
	b := extractors.ExtractedFact{
		SubjectText:        "Apple Inc.",
		PropertySlug:       "sec_cik",
		Value:              "0000320193",
		ValueType:          "string",
		Excerpt:            "Apple Inc. (CIK 0000320193)",
		ExcerptOffsetStart: 0,
		ExcerptOffsetEnd:   26,
		ExtractionMethod:   "regex:identifiers-v1",
		SourceConfidence:   &confidence,
	}

	if !reflect.DeepEqual(a, b) {
		t.Fatalf("expected facts to be equal: %#v vs %#v", a, b)
	}
}

func TestExtractorInterfaceAcceptsConcreteImplementations(t *testing.T) {
	t.Parallel()

	var _ extractors.Extractor = stubExtractor{}
}

func TestSourceRawText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		source *db.Source
		want   string
		ok     bool
	}{
		{
			name: "present",
			source: &db.Source{
				RawText: pgtype.Text{String: "hello world", Valid: true},
			},
			want: "hello world",
			ok:   true,
		},
		{
			name:   "missing",
			source: &db.Source{},
			want:   "",
			ok:     false,
		},
		{
			name: "nil source",
			ok:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := extractors.SourceRawText(tt.source)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}
