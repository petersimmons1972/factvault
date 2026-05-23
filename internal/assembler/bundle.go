package assembler

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Bundle is the canonical JSON structure produced by Assemble.
type Bundle struct {
	EntityID    string            `json:"entity_id"`
	Entities    []BundleEntity    `json:"entities"`
	Statements  []BundleStatement `json:"statements"`
	Sources     []BundleSource    `json:"sources"`
	Qualifiers  []BundleQualifier `json:"qualifiers,omitempty"`
	Relations   []BundleRelation  `json:"relations,omitempty"`
	AssembledAt time.Time         `json:"assembled_at"`
	TenantID    string            `json:"tenant_id"`
}

type BundleEntity struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	CanonicalName *string `json:"canonical_name,omitempty"`
	TypeURI       string  `json:"type_uri"`
	Description   *string `json:"description,omitempty"`
}

type BundleStatement struct {
	ID           string   `json:"id"`
	EntityID     string   `json:"entity_id"`
	PropertySlug string   `json:"property_slug"`
	Value        string   `json:"value"`
	ValueType    string   `json:"value_type"`
	Rank         int      `json:"rank"`
	Confidence   float64  `json:"confidence"`
	SourceIDs    []string `json:"source_ids"`
	QualifierIDs []string `json:"qualifier_ids,omitempty"`
}

type BundleSource struct {
	ID                 string  `json:"id"`
	URL                string  `json:"url"`
	ArchiveURL         *string `json:"archive_url,omitempty"`
	PublishedAt        *string `json:"published_at,omitempty"`
	VerificationStatus string  `json:"verification_status"`
	RawText            string  `json:"raw_text"`
	ExcerptOffsetStart int32   `json:"excerpt_offset_start"`
	ExcerptOffsetEnd   int32   `json:"excerpt_offset_end"`
}

type BundleQualifier struct {
	ID           string `json:"id"`
	StatementID  string `json:"statement_id"`
	PropertySlug string `json:"property_slug"`
	Value        string `json:"value"`
	ValueType    string `json:"value_type"`
}

type BundleRelation struct {
	ID               string  `json:"id"`
	SourceEntityID   string  `json:"source_entity_id"`
	TargetEntityID   string  `json:"target_entity_id"`
	RelationTypeSlug string  `json:"relation_type_slug"`
	Confidence       float64 `json:"confidence"`
}

// Errors
var (
	ErrEntityNotFound     = errors.New("entity not found")
	ErrTenantIsolation    = errors.New("tenant isolation violation")
	ErrInvalidDepth       = errors.New("depth must be between 0 and 3")
	ErrInvalidEntityCount = errors.New("must provide at least one entity ID")
)

// Assemble produces a Bundle from the database.
// ctx supports cancellation; tx is the database transaction.
// entityIDs are the seed entities; depth controls graph expansion (0-3).
// tenantID is enforced by RLS (caller must have set SET LOCAL app.tenant_id).
func Assemble(
	ctx context.Context,
	tx pgx.Tx,
	entityIDs []string,
	depth int,
	tenantID string,
) (*Bundle, error) {
	if len(entityIDs) == 0 {
		return nil, ErrInvalidEntityCount
	}
	if depth < 0 || depth > 3 {
		return nil, ErrInvalidDepth
	}

	allEntityIDs := entityIDs
	if depth > 0 {
		expanded, err := ExpandGraph(ctx, tx, entityIDs, depth, tenantID)
		if err != nil {
			return nil, err
		}
		allEntityIDs = expanded
	}

	entities, err := loadBundleEntities(ctx, tx, allEntityIDs, tenantID)
	if err != nil {
		return nil, err
	}
	if len(entities) != len(uniqueStrings(allEntityIDs)) {
		return nil, ErrEntityNotFound
	}

	statements, statementIDs, err := loadBundleStatements(ctx, tx, allEntityIDs, tenantID)
	if err != nil {
		return nil, err
	}

	sources, sourceIDsByStatement, err := loadBundleSources(ctx, tx, statementIDs, tenantID)
	if err != nil {
		return nil, err
	}
	for i := range statements {
		statements[i].SourceIDs = sourceIDsByStatement[statements[i].ID]
	}

	qualifiers, qualifierIDsByStatement, err := loadBundleQualifiers(ctx, tx, statementIDs)
	if err != nil {
		return nil, err
	}
	for i := range statements {
		statements[i].QualifierIDs = qualifierIDsByStatement[statements[i].ID]
	}

	relations, err := loadBundleRelations(ctx, tx, allEntityIDs, tenantID)
	if err != nil {
		return nil, err
	}

	return &Bundle{
		EntityID:    entityIDs[0],
		Entities:    entities,
		Statements:  statements,
		Sources:     sources,
		Qualifiers:  qualifiers,
		Relations:   relations,
		AssembledAt: time.Now().UTC(),
		TenantID:    tenantID,
	}, nil
}

func loadBundleEntities(ctx context.Context, tx pgx.Tx, entityIDs []string, tenantID string) ([]BundleEntity, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, label, ext_id, COALESCE(type_uri, ''), description
		FROM entities
		WHERE tenant_id = $1::uuid AND id = ANY($2::uuid[])
		ORDER BY label, id
	`, tenantID, uniqueStrings(entityIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var entities []BundleEntity
	for rows.Next() {
		var entity BundleEntity
		if err := rows.Scan(&entity.ID, &entity.Name, &entity.CanonicalName, &entity.TypeURI, &entity.Description); err != nil {
			return nil, err
		}
		entities = append(entities, entity)
	}
	return entities, rows.Err()
}

func loadBundleStatements(ctx context.Context, tx pgx.Tx, entityIDs []string, tenantID string) ([]BundleStatement, []string, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			s.id::text,
			s.subject_id::text,
			p.slug,
			p.value_type,
			s.val_entity::text,
			s.val_text,
			s.val_number::text,
			s.val_date,
			s.val_json::text,
			s.rank,
			s.confidence::float8
		FROM statements s
		JOIN properties p ON p.id = s.property_id
		WHERE s.tenant_id = $1::uuid AND s.subject_id = ANY($2::uuid[])
		ORDER BY
			CASE s.rank WHEN 'preferred' THEN 1 WHEN 'normal' THEN 2 ELSE 3 END,
			s.confidence DESC,
			s.created_at DESC,
			s.id
	`, tenantID, uniqueStrings(entityIDs))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var statements []BundleStatement
	var statementIDs []string
	for rows.Next() {
		var stmt BundleStatement
		var valEntity, valText, valNumber, valJSON pgtype.Text
		var valDate pgtype.Timestamptz
		var rank string
		if err := rows.Scan(
			&stmt.ID,
			&stmt.EntityID,
			&stmt.PropertySlug,
			&stmt.ValueType,
			&valEntity,
			&valText,
			&valNumber,
			&valDate,
			&valJSON,
			&rank,
			&stmt.Confidence,
		); err != nil {
			return nil, nil, err
		}
		stmt.Value = firstStatementValue(valEntity, valText, valNumber, valDate, valJSON)
		stmt.Rank = bundleRank(rank)
		statements = append(statements, stmt)
		statementIDs = append(statementIDs, stmt.ID)
	}
	return statements, statementIDs, rows.Err()
}

func loadBundleSources(ctx context.Context, tx pgx.Tx, statementIDs []string, tenantID string) ([]BundleSource, map[string][]string, error) {
	byStatement := make(map[string][]string)
	if len(statementIDs) == 0 {
		return nil, byStatement, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT
			ss.statement_id::text,
			s.id::text,
			s.url,
			s.archive_url,
			s.published_at,
			s.status,
			ss.excerpt,
			ss.excerpt_offset_start,
			ss.excerpt_offset_end
		FROM statement_sources ss
		JOIN sources s ON s.id = ss.source_id
		WHERE ss.tenant_id = $1::uuid AND ss.statement_id = ANY($2::uuid[])
		ORDER BY ss.statement_id, ss.extracted_at DESC, s.id
	`, tenantID, statementIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	seen := make(map[string]bool)
	var sources []BundleSource
	for rows.Next() {
		var statementID string
		var source BundleSource
		var publishedAt pgtype.Timestamptz
		if err := rows.Scan(
			&statementID,
			&source.ID,
			&source.URL,
			&source.ArchiveURL,
			&publishedAt,
			&source.VerificationStatus,
			&source.RawText,
			&source.ExcerptOffsetStart,
			&source.ExcerptOffsetEnd,
		); err != nil {
			return nil, nil, err
		}
		if publishedAt.Valid {
			formatted := publishedAt.Time.UTC().Format(time.RFC3339)
			source.PublishedAt = &formatted
		}
		byStatement[statementID] = append(byStatement[statementID], source.ID)
		if !seen[source.ID] {
			seen[source.ID] = true
			sources = append(sources, source)
		}
	}
	return sources, byStatement, rows.Err()
}

func loadBundleQualifiers(ctx context.Context, tx pgx.Tx, statementIDs []string) ([]BundleQualifier, map[string][]string, error) {
	byStatement := make(map[string][]string)
	if len(statementIDs) == 0 {
		return nil, byStatement, nil
	}

	rows, err := tx.Query(ctx, `
		SELECT
			q.id::text,
			q.statement_id::text,
			p.slug,
			p.value_type,
			q.val_entity::text,
			q.val_text,
			q.val_number::text,
			q.val_date
		FROM qualifiers q
		JOIN properties p ON p.id = q.property_id
		WHERE q.statement_id = ANY($1::uuid[])
		ORDER BY q.statement_id, p.slug, q.id
	`, statementIDs)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var qualifiers []BundleQualifier
	for rows.Next() {
		var qualifier BundleQualifier
		var valEntity, valText, valNumber pgtype.Text
		var valDate pgtype.Timestamptz
		if err := rows.Scan(
			&qualifier.ID,
			&qualifier.StatementID,
			&qualifier.PropertySlug,
			&qualifier.ValueType,
			&valEntity,
			&valText,
			&valNumber,
			&valDate,
		); err != nil {
			return nil, nil, err
		}
		qualifier.Value = firstStatementValue(valEntity, valText, valNumber, valDate, pgtype.Text{})
		qualifiers = append(qualifiers, qualifier)
		byStatement[qualifier.StatementID] = append(byStatement[qualifier.StatementID], qualifier.ID)
	}
	return qualifiers, byStatement, rows.Err()
}

func loadBundleRelations(ctx context.Context, tx pgx.Tx, entityIDs []string, tenantID string) ([]BundleRelation, error) {
	rows, err := tx.Query(ctx, `
		SELECT id::text, source_id::text, target_id::text, type, COALESCE(confidence::float8, 0)
		FROM relations
		WHERE tenant_id = $1::uuid
		  AND source_id = ANY($2::uuid[])
		  AND target_id = ANY($2::uuid[])
		ORDER BY confidence DESC NULLS LAST, id
	`, tenantID, uniqueStrings(entityIDs))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var relations []BundleRelation
	for rows.Next() {
		var relation BundleRelation
		if err := rows.Scan(
			&relation.ID,
			&relation.SourceEntityID,
			&relation.TargetEntityID,
			&relation.RelationTypeSlug,
			&relation.Confidence,
		); err != nil {
			return nil, err
		}
		relations = append(relations, relation)
	}
	return relations, rows.Err()
}

// MarshalJSON marshals the Bundle to JSON bytes.
func (b *Bundle) MarshalJSON() ([]byte, error) {
	type alias Bundle
	return json.Marshal((*alias)(b))
}

// ConvertUUIDToString safely converts pgtype.UUID to string.
func ConvertUUIDToString(u pgtype.UUID) string {
	if !u.Valid {
		return ""
	}
	return u.String()
}

// ConvertTextToString safely converts pgtype.Text to string.
func ConvertTextToString(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	return &t.String
}

func firstStatementValue(valEntity, valText, valNumber pgtype.Text, valDate pgtype.Timestamptz, valJSON pgtype.Text) string {
	switch {
	case valEntity.Valid:
		return valEntity.String
	case valText.Valid:
		return valText.String
	case valNumber.Valid:
		return valNumber.String
	case valDate.Valid:
		return valDate.Time.UTC().Format(time.RFC3339)
	case valJSON.Valid:
		return valJSON.String
	default:
		return ""
	}
}

func bundleRank(rank string) int {
	switch rank {
	case "preferred":
		return 1
	case "normal":
		return 2
	case "deprecated":
		return 3
	default:
		return 2
	}
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	return unique
}
