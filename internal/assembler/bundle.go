package assembler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	// TODO: Implement full assembler logic
	// For now, return a stub to enable testing the structure
	return nil, fmt.Errorf("Assemble not yet implemented")
}

// MarshalJSON marshals the Bundle to JSON bytes.
func (b *Bundle) MarshalJSON() ([]byte, error) {
	return json.Marshal(b)
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
