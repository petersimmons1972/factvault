package workers

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
)

// VerifyExcerptOffset validates that the excerpt appears at the claimed offsets in the source text.
func VerifyExcerptOffset(rawText, excerpt string, offsetStart, offsetEnd int) bool {
	if offsetStart < 0 || offsetEnd < 0 || offsetStart >= offsetEnd {
		return false
	}
	startBytePos, ok := runeOffsetToByteOffset(rawText, offsetStart)
	if !ok {
		return false
	}
	endBytePos, ok := runeOffsetToByteOffset(rawText, offsetEnd)
	if !ok || startBytePos > endBytePos || endBytePos > len(rawText) {
		return false
	}
	return rawText[startBytePos:endBytePos] == excerpt
}

type FactPipeline struct {
	DB            *pgxpool.Pool
	Deterministic extractors.Extractor
	LLM           *extractors.LLMClient
	Logger        *slog.Logger
}

func (p *FactPipeline) ExtractOnce(ctx context.Context, tenantID string, limit int) error {
	if p == nil || p.DB == nil {
		return fmt.Errorf("extract worker: nil db pool")
	}
	if tenantID == "" {
		return fmt.Errorf("tenant id required")
	}
	if limit <= 0 {
		limit = 100
	}
	logger := p.logger()
	rows, err := p.DB.Query(ctx, `
		SELECT id, tenant_id, url, fetched_at, content_hash, raw_html, raw_text, archive_url, publisher, title, published_at, last_verified_at, status, created_at
		FROM sources
		WHERE tenant_id = $1::uuid AND raw_text IS NOT NULL AND status IN ('archived', 'verified', 'content-changed')
		ORDER BY fetched_at ASC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var source db.Source
		if err := rows.Scan(
			&source.ID, &source.TenantID, &source.Url, &source.FetchedAt, &source.ContentHash, &source.RawHtml, &source.RawText,
			&source.ArchiveUrl, &source.Publisher, &source.Title, &source.PublishedAt, &source.LastVerifiedAt, &source.Status, &source.CreatedAt,
		); err != nil {
			return err
		}
		rawText, ok := extractors.SourceRawText(&source)
		if !ok || rawText == "" {
			continue
		}
		facts, err := p.extractFacts(ctx, &source, rawText)
		if err != nil {
			return err
		}
		for _, fact := range facts {
			if !VerifyExcerptOffset(rawText, fact.Excerpt, fact.ExcerptOffsetStart, fact.ExcerptOffsetEnd) {
				logger.WarnContext(ctx, "rejecting fact with invalid excerpt offset", "source_id", source.ID.String(), "property", fact.PropertySlug)
				continue
			}
			if err := p.insertFact(ctx, tenantID, source.ID.String(), fact); err != nil {
				return err
			}
		}
		if _, err := p.DB.Exec(ctx, "UPDATE sources SET status = 'extracted' WHERE id = $1", source.ID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (p *FactPipeline) extractFacts(ctx context.Context, source *db.Source, rawText string) ([]extractors.ExtractedFact, error) {
	runner := p.Deterministic
	if runner == nil {
		r := deterministic.NewRunner()
		runner = r
	}
	facts, err := runner.Extract(ctx, source, rawText)
	if err != nil {
		return nil, err
	}
	if p.LLM != nil {
		proposals, err := p.LLM.Extract(ctx, source, rawText)
		if err != nil {
			return nil, err
		}
		for _, proposal := range extractors.FilterVerifiedStatementProposals(rawText, proposals) {
			facts = append(facts, extractors.ExtractedFact{
				SubjectText:        proposal.SubjectText,
				PropertySlug:       proposal.PropertySlug,
				Value:              proposal.Value,
				ValueType:          proposal.ValueType,
				Excerpt:            proposal.Excerpt,
				ExcerptOffsetStart: proposal.ExcerptOffsetStart,
				ExcerptOffsetEnd:   proposal.ExcerptOffsetEnd,
				ExtractionMethod:   "llm:v1:covered-span",
			})
		}
	}
	return facts, nil
}

func (p *FactPipeline) insertFact(ctx context.Context, tenantID, sourceID string, fact extractors.ExtractedFact) error {
	subjectID, err := p.ensureEntity(ctx, tenantID, fact.SubjectText)
	if err != nil {
		return err
	}
	propertyID, valueType, err := p.ensureProperty(ctx, tenantID, fact.PropertySlug, fact.ValueType)
	if err != nil {
		return err
	}
	statementID := uuid.NewString()
	confidence := 0.5
	if fact.SourceConfidence != nil {
		confidence = *fact.SourceConfidence
	}
	query, args := insertStatementQuery(statementID, tenantID, subjectID, propertyID, valueType, fact.Value, confidence)
	if _, err := p.DB.Exec(ctx, query, args...); err != nil {
		return err
	}
	_, err = p.DB.Exec(ctx, `
		INSERT INTO statement_sources (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.NewString(), statementID, sourceID, fact.Excerpt, fact.ExcerptOffsetStart, fact.ExcerptOffsetEnd, fact.ExtractionMethod, confidence, tenantID)
	return err
}

func (p *FactPipeline) ensureEntity(ctx context.Context, tenantID, label string) (string, error) {
	if label == "" {
		label = "unknown subject"
	}
	extID := "extracted:" + label
	var id string
	err := p.DB.QueryRow(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri)
		VALUES ($1, $2, $3, $4, 'https://schema.org/Thing')
		ON CONFLICT (tenant_id, ext_id) DO UPDATE SET label = EXCLUDED.label
		RETURNING id::text
	`, uuid.NewString(), tenantID, extID, label).Scan(&id)
	return id, err
}

func (p *FactPipeline) ensureProperty(ctx context.Context, tenantID, slug, valueType string) (string, string, error) {
	if slug == "" {
		slug = "extracted_fact"
	}
	valueType = normalizeValueType(valueType)
	var id string
	err := p.DB.QueryRow(ctx, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1, $2, $3, $3, $4)
		ON CONFLICT (tenant_id, slug) DO UPDATE SET value_type = EXCLUDED.value_type
		RETURNING id::text
	`, uuid.NewString(), tenantID, slug, valueType).Scan(&id)
	return id, valueType, err
}

func insertStatementQuery(statementID, tenantID, subjectID, propertyID, valueType, value string, confidence float64) (string, []any) {
	base := `INSERT INTO statements (id, tenant_id, subject_id, property_id, %s, rank, confidence) VALUES ($1, $2, $3, $4, %s, 'normal', $6)`
	args := []any{statementID, tenantID, subjectID, propertyID, value, confidence}
	switch valueType {
	case "number":
		return fmt.Sprintf(base, "val_number", "$5::numeric"), args
	case "date":
		return fmt.Sprintf(base, "val_date", "$5::timestamptz"), args
	case "entity_ref":
		return fmt.Sprintf(base, "val_entity", "$5::uuid"), args
	default:
		return fmt.Sprintf(base, "val_text", "$5"), args
	}
}

func normalizeValueType(valueType string) string {
	switch valueType {
	case "entity_ref", "string", "number", "date", "url":
		return valueType
	default:
		return "string"
	}
}

func (p *FactPipeline) logger() *slog.Logger {
	if p.Logger != nil {
		return p.Logger
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, nil))
}

// ExtractOnce preserves the original package-level smoke-test entry point.
func ExtractOnce(ctx context.Context, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(os.Stdout, nil))
	}
	logger.InfoContext(ctx, "extract worker requires FactPipeline for database-backed execution")
	return nil
}

func runeOffsetToByteOffset(rawText string, runeOffset int) (int, bool) {
	if runeOffset == 0 {
		return 0, true
	}
	if runeOffset < 0 {
		return 0, false
	}
	index := 0
	for byteOffset := range rawText {
		if index == runeOffset {
			return byteOffset, true
		}
		index++
	}
	if index == runeOffset {
		return len(rawText), true
	}
	return 0, false
}
