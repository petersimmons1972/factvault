package workers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/extractors"
	"github.com/petersimmons1972/factvault/internal/extractors/deterministic"
	"github.com/petersimmons1972/factvault/internal/vocabulary"
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

// FactPipeline orchestrates extraction from archived source text into statements.
type FactPipeline struct {
	DB                     *pgxpool.Pool
	Deterministic          extractors.Extractor
	LLM                    LLMExtractor
	LLMProvider            string
	CostGuardrailThreshold int
	ConfirmCost            bool
	VocabularyMode         vocabulary.Mode
	Logger                 *slog.Logger
}

// LLMExtractor submits source text to an LLM and returns statement proposals.
type LLMExtractor interface {
	Extract(ctx context.Context, source *db.Source, rawText string) ([]extractors.StatementProposal, error)
}

type queryExecutor interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// ExtractOnce runs deterministic extraction (and optional LLM augmentation) once.
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
	if err := p.costGuardrail(limit); err != nil {
		return err
	}
	var tenant pgtype.UUID
	if err := tenant.Scan(tenantID); err != nil {
		return fmt.Errorf("invalid tenant id: %w", err)
	}
	logger := p.logger()
	sources, err := p.loadArchivedSources(ctx, tenant, tenantID, limit)
	if err != nil {
		return err
	}

	for _, source := range sources {
		rawText, ok := extractors.SourceRawText(&source)
		if !ok || rawText == "" {
			continue
		}
		facts, err := p.extractFacts(ctx, &source, rawText)
		if err != nil {
			return err
		}
		if err := p.writeExtractedFacts(ctx, tenant, tenantID, &source, rawText, facts, logger); err != nil {
			return err
		}
	}
	return nil
}

func (p *FactPipeline) loadArchivedSources(ctx context.Context, tenant pgtype.UUID, tenantID string, limit int) ([]db.Source, error) {
	txCtx := db.WithPool(ctx, p.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			fmt.Fprintf(os.Stderr, "extract worker: rollback read tx: %v\n", err)
		}
	}()

	rows, err := tx.Query(txCtx, `
		SELECT id, tenant_id, url, fetched_at, content_hash, raw_html, raw_text, archive_url, publisher, title, published_at, last_verified_at, status, created_at
		FROM sources
		WHERE tenant_id = $1::uuid AND raw_text IS NOT NULL AND status = 'archived'
		ORDER BY fetched_at ASC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sources []db.Source
	for rows.Next() {
		var source db.Source
		if err := rows.Scan(
			&source.ID, &source.TenantID, &source.Url, &source.FetchedAt, &source.ContentHash, &source.RawHtml, &source.RawText,
			&source.ArchiveUrl, &source.Publisher, &source.Title, &source.PublishedAt, &source.LastVerifiedAt, &source.Status, &source.CreatedAt,
		); err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	if err := tx.Commit(txCtx); err != nil {
		return nil, err
	}
	return sources, nil
}

func (p *FactPipeline) writeExtractedFacts(
	ctx context.Context,
	tenant pgtype.UUID,
	tenantID string,
	source *db.Source,
	rawText string,
	facts []extractors.ExtractedFact,
	logger *slog.Logger,
) error {
	if len(facts) == 0 {
		return nil
	}

	txCtx := db.WithPool(ctx, p.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenant)
	if err != nil {
		return err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			fmt.Fprintf(os.Stderr, "extract worker: rollback write tx: %v\n", err)
		}
	}()

	acceptedFacts := 0
	for _, fact := range facts {
		if !VerifyExcerptOffset(rawText, fact.Excerpt, fact.ExcerptOffsetStart, fact.ExcerptOffsetEnd) {
			logger.WarnContext(ctx, "rejecting fact with invalid excerpt offset", "source_id", source.ID.String(), "property", fact.PropertySlug)
			continue
		}
		inserted, err := p.insertFact(txCtx, tx, tenantID, source.ID.String(), fact)
		if err != nil {
			return err
		}
		if inserted {
			acceptedFacts++
		}
	}
	if acceptedFacts > 0 {
		if _, err := tx.Exec(txCtx, "UPDATE sources SET status = 'extracted' WHERE id = $1", source.ID); err != nil {
			return err
		}
	}
	return tx.Commit(txCtx)
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

func (p *FactPipeline) insertFact(ctx context.Context, exec queryExecutor, tenantID, sourceID string, fact extractors.ExtractedFact) (bool, error) {
	subjectID, err := p.ensureEntity(ctx, exec, tenantID, fact.SubjectText)
	if err != nil {
		return false, err
	}
	propertyID, valueType, shouldWrite, err := p.ensureProperty(ctx, exec, tenantID, sourceID, fact)
	if err != nil {
		return false, err
	}
	if !shouldWrite {
		return false, nil
	}
	statementID := uuid.NewString()
	confidence := 0.5
	if fact.SourceConfidence != nil {
		confidence = *fact.SourceConfidence
	}
	query, args := insertStatementQuery(statementID, tenantID, subjectID, propertyID, valueType, fact.Value, confidence)
	if _, err := exec.Exec(ctx, query, args...); err != nil {
		return false, err
	}
	_, err = exec.Exec(ctx, `
		INSERT INTO statement_sources (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method, confidence, tenant_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`, uuid.NewString(), statementID, sourceID, fact.Excerpt, fact.ExcerptOffsetStart, fact.ExcerptOffsetEnd, fact.ExtractionMethod, confidence, tenantID)
	if err != nil {
		return false, err
	}
	return true, nil
}

func (p *FactPipeline) ensureEntity(ctx context.Context, exec queryExecutor, tenantID, label string) (string, error) {
	if label == "" {
		label = "unknown subject"
	}
	extID := entityExtIDForLabel(label)
	var id string
	err := exec.QueryRow(ctx, `
		SELECT id::text
		FROM entities
		WHERE tenant_id = $1 AND label = $2
		ORDER BY created_at ASC
		LIMIT 1
	`, tenantID, label).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	err = exec.QueryRow(ctx, `
		INSERT INTO entities (id, tenant_id, ext_id, label, type_uri)
		VALUES ($1, $2, $3, $4, NULL)
		RETURNING id::text
	`, uuid.NewString(), tenantID, extID, label).Scan(&id)
	return id, err
}

func entityExtIDForLabel(label string) string {
	normalized := strings.TrimSpace(strings.ToLower(label))
	sum := sha256.Sum256([]byte(normalized))
	return "extract:subject:" + hex.EncodeToString(sum[:16])
}

func (p *FactPipeline) ensureProperty(ctx context.Context, exec queryExecutor, tenantID, sourceID string, fact extractors.ExtractedFact) (string, string, bool, error) {
	resolver := vocabulary.NewResolver(p.vocabularyMode())
	result := resolver.Resolve(fact.PropertySlug, fact.ValueType, fact.Excerpt)
	slug := result.Property.Slug
	valueType := normalizeValueType(result.Property.ValueType)
	if valueType == "" {
		return "", "", false, fmt.Errorf("unsupported property value type %q", result.Property.ValueType)
	}
	var id string
	err := exec.QueryRow(ctx, `
		SELECT id::text
		FROM properties
		WHERE slug = $1 AND (tenant_id = $2 OR tenant_id IS NULL)
		ORDER BY tenant_id NULLS LAST
		LIMIT 1
	`, slug, tenantID).Scan(&id)
	if err == nil {
		return id, valueType, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, err
	}

	if !result.Known && p.vocabularyMode() == vocabulary.ModeStrict {
		_, err := exec.Exec(ctx, `
			INSERT INTO proposed_properties (
				id, tenant_id, proposed_slug, proposed_value_type, proposed_by,
				example_excerpt, example_source_id
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT DO NOTHING
		`, uuid.NewString(), tenantID, slug, valueType, "extract:strict", fact.Excerpt, sourceID)
		return "", valueType, false, err
	}

	label := result.Property.Label
	if !result.Known {
		label = slug
	}
	err = exec.QueryRow(ctx, `
		INSERT INTO properties (id, tenant_id, slug, label, value_type)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id::text
	`, uuid.NewString(), tenantID, slug, label, valueType).Scan(&id)
	return id, valueType, true, err
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
	switch strings.ToLower(strings.TrimSpace(valueType)) {
	case "entity_ref", "string", "number", "date", "url":
		return strings.ToLower(strings.TrimSpace(valueType))
	default:
		return "string"
	}
}

func (p *FactPipeline) vocabularyMode() vocabulary.Mode {
	if p == nil || p.VocabularyMode == "" {
		return vocabulary.ModeStrict
	}
	return p.VocabularyMode
}

func (p *FactPipeline) costGuardrail(limit int) error {
	if p == nil || p.LLM == nil {
		return nil
	}
	provider := strings.ToLower(strings.TrimSpace(p.LLMProvider))
	switch provider {
	case "", "local", "ollama":
		return nil
	case "anthropic", "openai":
	default:
		return nil
	}

	threshold := p.CostGuardrailThreshold
	if threshold == 0 {
		threshold = 1000
	}
	if threshold < 100 || threshold > 10000 {
		return fmt.Errorf("invalid cost guardrail threshold %d: must be between 100 and 10000", threshold)
	}
	if p.ConfirmCost || os.Getenv("FACTVAULT_CONFIRM_COST") == "1" {
		return nil
	}
	if limit > threshold {
		return fmt.Errorf("cost guardrail blocked: provider=%s limit=%d threshold=%d; set --confirm-cost or FACTVAULT_CONFIRM_COST=1", provider, limit, threshold)
	}
	return nil
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
