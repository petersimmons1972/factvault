//go:build sqlite && cgo

// Package sqlite implements store interfaces using SQLite and sqlite-vec.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	"github.com/jackc/pgx/v5/pgtype"
	// Register the database/sql sqlite3 driver required by this backend.
	_ "github.com/mattn/go-sqlite3"
	pgvector "github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/store"
)

var (
	errInvalidLimit = errors.New("limit must be greater than zero")
	registerVecOnce sync.Once
)

// Store is the SQLite implementation of the backend-neutral store interfaces.
type Store struct {
	db *sql.DB
}

// Open opens a SQLite database and applies native SQLite migrations.
//
// It uses database/sql because mattn/go-sqlite3 is the required SQLite driver
// for this backend.
func Open(ctx context.Context, dsn string) (*Store, error) {
	registerVecOnce.Do(sqlitevec.Auto)

	sqlDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB.SetMaxOpenConns(1)

	s := &Store{db: sqlDB}
	if err := s.migrate(ctx); err != nil {
		if closeErr := sqlDB.Close(); closeErr != nil {
			return nil, errors.Join(err, closeErr)
		}
		return nil, err
	}
	return s, nil
}

// Close closes the SQLite database handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// EncodeVector converts a float32 embedding into sqlite-vec's compact BLOB format.
func EncodeVector(vector []float32) []byte {
	encoded, err := sqlitevec.SerializeFloat32(vector)
	if err != nil {
		panic(fmt.Sprintf("encode sqlite vector: %v", err))
	}
	return encoded
}

// GetEntity loads one entity by ID.
func (s *Store) GetEntity(ctx context.Context, id pgtype.UUID) (db.Entity, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE id = ?
`, uuidString(id))
	return scanEntity(row)
}

// ListEntitiesByTenant lists entities scoped by tenant_id.
func (s *Store) ListEntitiesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Entity, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE tenant_id = ?
ORDER BY created_at DESC
`, uuidString(tenantID))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)
	return scanEntities(rows)
}

// ListPropertiesByTenant lists tenant properties plus global properties.
func (s *Store) ListPropertiesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Property, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id, slug, label, value_type, description
FROM properties
WHERE tenant_id = ? OR tenant_id IS NULL
ORDER BY slug
`, uuidString(tenantID))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var out []db.Property
	for rows.Next() {
		var property db.Property
		var id, tenantIDValue, description sql.NullString
		if err := rows.Scan(&id, &tenantIDValue, &property.Slug, &property.Label, &property.ValueType, &description); err != nil {
			return nil, err
		}
		property.ID = parseUUID(id.String)
		property.TenantID = parseNullableUUID(tenantIDValue)
		property.Description = parseText(description)
		out = append(out, property)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListQualifiersByStatement lists qualifiers for a statement.
func (s *Store) ListQualifiersByStatement(ctx context.Context, statementID pgtype.UUID) ([]db.Qualifier, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, statement_id, property_id, val_text, val_number, val_date, val_entity
FROM qualifiers
WHERE statement_id = ?
`, uuidString(statementID))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var out []db.Qualifier
	for rows.Next() {
		var qualifier db.Qualifier
		var id, stmtID, propertyID, valText, valNumber, valDate, valEntity sql.NullString
		if err := rows.Scan(&id, &stmtID, &propertyID, &valText, &valNumber, &valDate, &valEntity); err != nil {
			return nil, err
		}
		qualifier.ID = parseUUID(id.String)
		qualifier.StatementID = parseUUID(stmtID.String)
		qualifier.PropertyID = parseUUID(propertyID.String)
		qualifier.ValText = parseText(valText)
		qualifier.ValNumber = parseNumeric(valNumber)
		qualifier.ValDate = parseTimestamptz(valDate)
		qualifier.ValEntity = parseNullableUUID(valEntity)
		out = append(out, qualifier)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListSourcesByTenant lists sources scoped by tenant_id.
func (s *Store) ListSourcesByTenant(ctx context.Context, tenantID pgtype.UUID) ([]db.Source, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id, url, fetched_at, content_hash, raw_html, raw_text, archive_url, publisher, title, published_at, embedding, last_verified_at, status, created_at
FROM sources
WHERE tenant_id = ?
ORDER BY fetched_at DESC
`, uuidString(tenantID))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var out []db.Source
	for rows.Next() {
		source, err := scanSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, source)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ListStatementsBySubject lists statements for a subject entity.
func (s *Store) ListStatementsBySubject(ctx context.Context, subjectID pgtype.UUID) ([]db.Statement, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id, subject_id, property_id, val_entity, val_text, val_number, val_date, val_json, rank, confidence, embedding, created_at
FROM statements
WHERE subject_id = ?
ORDER BY created_at DESC
`, uuidString(subjectID))
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var out []db.Statement
	for rows.Next() {
		statement, err := scanStatement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, statement)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchNearest returns nearest entities for a tenant using sqlite-vec cosine distance.
func (s *Store) SearchNearest(ctx context.Context, tenantID pgtype.UUID, embedding []float32, k int) ([]store.EntityWithScore, error) {
	if k <= 0 {
		return nil, errInvalidLimit
	}
	queryVector := EncodeVector(embedding)
	rows, err := s.db.QueryContext(ctx, `
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at,
       1.0 - vec_distance_cosine(embedding, ?) AS score
FROM entities
WHERE tenant_id = ?
  AND embedding IS NOT NULL
ORDER BY vec_distance_cosine(embedding, ?), created_at DESC
LIMIT ?
`, queryVector, uuidString(tenantID), queryVector, k)
	if err != nil {
		return nil, err
	}
	defer closeRows(rows)

	var out []store.EntityWithScore
	for rows.Next() {
		entity, score, err := scanEntityWithScore(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, store.EntityWithScore{Entity: entity, Score: score})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanEntities(rows *sql.Rows) ([]db.Entity, error) {
	var out []db.Entity
	for rows.Next() {
		entity, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, entity)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func scanEntity(row scanner) (db.Entity, error) {
	entity, _, err := scanEntityWithOptionalScore(row, false)
	return entity, err
}

func scanEntityWithScore(row scanner) (db.Entity, float64, error) {
	return scanEntityWithOptionalScore(row, true)
}

func scanEntityWithOptionalScore(row scanner, withScore bool) (db.Entity, float64, error) {
	var entity db.Entity
	var id, tenantID, extID, typeURI, description, createdAt, updatedAt sql.NullString
	var embedding, meta []byte
	var score sql.NullFloat64
	dest := []any{&id, &tenantID, &extID, &entity.Label, &typeURI, &description, &embedding, &meta, &createdAt, &updatedAt}
	if withScore {
		dest = append(dest, &score)
	}
	if err := row.Scan(dest...); err != nil {
		return db.Entity{}, 0, err
	}
	entity.ID = parseUUID(id.String)
	entity.TenantID = parseUUID(tenantID.String)
	entity.ExtID = parseText(extID)
	entity.TypeUri = parseText(typeURI)
	entity.Description = parseText(description)
	entity.Embedding = parseVector(embedding)
	entity.Meta = defaultJSON(meta)
	entity.CreatedAt = parseTimestamptz(createdAt)
	entity.UpdatedAt = parseTimestamptz(updatedAt)
	return entity, score.Float64, nil
}

func scanSource(row scanner) (db.Source, error) {
	var source db.Source
	var id, tenantID, fetchedAt, rawText, archiveURL, publisher, title, publishedAt, lastVerifiedAt, createdAt sql.NullString
	var rawHTML, embedding []byte
	if err := row.Scan(
		&id, &tenantID, &source.Url, &fetchedAt, &source.ContentHash, &rawHTML, &rawText, &archiveURL,
		&publisher, &title, &publishedAt, &embedding, &lastVerifiedAt, &source.Status, &createdAt,
	); err != nil {
		return db.Source{}, err
	}
	source.ID = parseUUID(id.String)
	source.TenantID = parseUUID(tenantID.String)
	source.FetchedAt = parseTimestamptz(fetchedAt)
	source.RawHtml = rawHTML
	source.RawText = parseText(rawText)
	source.ArchiveUrl = parseText(archiveURL)
	source.Publisher = parseText(publisher)
	source.Title = parseText(title)
	source.PublishedAt = parseTimestamptz(publishedAt)
	source.Embedding = parseVector(embedding)
	source.LastVerifiedAt = parseTimestamptz(lastVerifiedAt)
	source.CreatedAt = parseTimestamptz(createdAt)
	return source, nil
}

func scanStatement(row scanner) (db.Statement, error) {
	var statement db.Statement
	var id, tenantID, subjectID, propertyID, valEntity, valText, valNumber, valDate, createdAt sql.NullString
	var confidence sql.NullString
	var valJSON, embedding []byte
	if err := row.Scan(
		&id, &tenantID, &subjectID, &propertyID, &valEntity, &valText, &valNumber, &valDate,
		&valJSON, &statement.Rank, &confidence, &embedding, &createdAt,
	); err != nil {
		return db.Statement{}, err
	}
	statement.ID = parseUUID(id.String)
	statement.TenantID = parseUUID(tenantID.String)
	statement.SubjectID = parseUUID(subjectID.String)
	statement.PropertyID = parseUUID(propertyID.String)
	statement.ValEntity = parseNullableUUID(valEntity)
	statement.ValText = parseText(valText)
	statement.ValNumber = parseNumeric(valNumber)
	statement.ValDate = parseTimestamptz(valDate)
	statement.ValJson = valJSON
	statement.Confidence = parseNumeric(confidence)
	statement.Embedding = parseVector(embedding)
	statement.CreatedAt = parseTimestamptz(createdAt)
	return statement, nil
}

func parseUUID(value string) pgtype.UUID {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil {
		return pgtype.UUID{}
	}
	return id
}

func parseNullableUUID(value sql.NullString) pgtype.UUID {
	if !value.Valid || value.String == "" {
		return pgtype.UUID{}
	}
	return parseUUID(value.String)
}

func parseText(value sql.NullString) pgtype.Text {
	if !value.Valid {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value.String, Valid: true}
}

func parseNumeric(value sql.NullString) pgtype.Numeric {
	if !value.Valid || value.String == "" {
		return pgtype.Numeric{}
	}
	var numeric pgtype.Numeric
	if err := numeric.Scan(value.String); err != nil {
		return pgtype.Numeric{}
	}
	return numeric
}

func parseTimestamptz(value sql.NullString) pgtype.Timestamptz {
	if !value.Valid || value.String == "" {
		return pgtype.Timestamptz{}
	}
	var ts pgtype.Timestamptz
	if err := ts.Scan(value.String); err != nil {
		return pgtype.Timestamptz{}
	}
	return ts
}

func parseVector(value []byte) pgvector.Vector {
	if len(value) == 0 {
		return pgvector.Vector{}
	}
	floats, err := decodeVector(value)
	if err != nil {
		return pgvector.Vector{}
	}
	return pgvector.NewVector(floats)
}

func decodeVector(value []byte) ([]float32, error) {
	if len(value)%4 != 0 {
		return nil, fmt.Errorf("sqlite vector blob length %d is not divisible by 4", len(value))
	}
	floats := make([]float32, len(value)/4)
	for i := range floats {
		bits := uint32(value[i*4]) | uint32(value[i*4+1])<<8 | uint32(value[i*4+2])<<16 | uint32(value[i*4+3])<<24
		floats[i] = math.Float32frombits(bits)
	}
	return floats, nil
}

func defaultJSON(value []byte) []byte {
	if len(value) == 0 || !json.Valid(value) {
		return []byte(`{}`)
	}
	return value
}

func uuidString(id pgtype.UUID) string {
	return id.String()
}

func closeRows(rows *sql.Rows) {
	if err := rows.Close(); err != nil {
		return
	}
}
