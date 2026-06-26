// Package briefs generates and persists evidence briefs from assembled bundles.
// A brief is a structured JSON document derived from a Bundle that surfaces
// key claims, citation metadata, detected conflicts, source health, evidence gaps,
// and writer prompts. Briefs are tenant-scoped via RLS on evidence_briefs.
package briefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/petersimmons1972/factvault/internal/assembler"
)

// SourceKind classifies what produced the brief.
type SourceKind string

const (
	// SourceKindDossier indicates a brief derived from an entity dossier.
	SourceKindDossier SourceKind = "dossier"
	// SourceKindStory indicates a brief derived from a story query.
	SourceKindStory SourceKind = "story"
)

// GenerateRequest carries all inputs needed to generate and store a brief.
type GenerateRequest struct {
	SourceKind SourceKind        `json:"source_kind"`
	EntityID   *string           `json:"entity_id,omitempty"`
	Query      *string           `json:"query,omitempty"`
	Bundle     *assembler.Bundle `json:"bundle"`
}

// ListOptions controls which briefs are returned from List.
type ListOptions struct {
	Limit         int
	SourceKind    *SourceKind
	EntityID      *string
	CreatedAfter  *time.Time
	CreatedBefore *time.Time
}

// Brief is the stored record returned by GenerateAndStore, List, and Get.
type Brief struct {
	ID         string
	SourceKind SourceKind
	EntityID   *string
	Payload    []byte // raw brief JSON
	// BundleHash is an optional content-addressed hash of the assembled bundle.
	// Populated when the brief is derived from a deduplicated bundle run.
	// NULL means the brief was stored before bundle dedup was introduced (migration 00004).
	BundleHash *string
}

// BriefGenerator derives a deterministic JSON brief from a Bundle.
type BriefGenerator struct{}

// briefDoc is the fixed-field struct that becomes the JSON payload.
// Using a named struct guarantees stable key ordering in encoding/json output.
type briefDoc struct {
	KeyClaims     []keyClaim    `json:"key_claims"`
	Citations     []citation    `json:"citations"`
	Conflicts     []conflict    `json:"conflicts"`
	SourceHealth  []sourceEntry `json:"source_health"`
	EvidenceGaps  []evidenceGap `json:"evidence_gaps"`
	WriterPrompts []string      `json:"writer_prompts"`
}

type keyClaim struct {
	StatementID  string  `json:"statement_id"`
	EntityID     string  `json:"entity_id"`
	PropertySlug string  `json:"property_slug"`
	Value        string  `json:"value"`
	Confidence   float64 `json:"confidence"`
}

type citation struct {
	SourceID           string `json:"source_id"`
	URL                string `json:"url"`
	VerificationStatus string `json:"verification_status"`
}

type conflict struct {
	EntityID     string `json:"entity_id"`
	PropertySlug string `json:"property_slug"`
	ValueA       string `json:"value_a"`
	ValueB       string `json:"value_b"`
}

type sourceEntry struct {
	SourceID string `json:"source_id"`
	Status   string `json:"status"`
}

type evidenceGap struct {
	StatementID  string `json:"statement_id"`
	PropertySlug string `json:"property_slug"`
}

// Generate builds a deterministic JSON brief from b.
// The output is stable across repeated calls for the same input.
func (g BriefGenerator) Generate(b *assembler.Bundle) ([]byte, error) {
	doc := briefDoc{
		KeyClaims:     extractKeyClaims(b),
		Citations:     extractCitations(b),
		Conflicts:     detectConflicts(b),
		SourceHealth:  buildSourceHealth(b),
		EvidenceGaps:  findEvidenceGaps(b),
		WriterPrompts: deriveWriterPrompts(b),
	}
	return json.Marshal(doc)
}

// extractKeyClaims returns statements with confidence >= 0.7, sorted deterministically.
func extractKeyClaims(b *assembler.Bundle) []keyClaim {
	var claims []keyClaim
	for _, s := range b.Statements {
		if s.Confidence >= 0.7 {
			claims = append(claims, keyClaim{
				StatementID:  s.ID,
				EntityID:     s.EntityID,
				PropertySlug: s.PropertySlug,
				Value:        s.Value,
				Confidence:   s.Confidence,
			})
		}
	}
	sort.Slice(claims, func(i, j int) bool {
		if claims[i].StatementID != claims[j].StatementID {
			return claims[i].StatementID < claims[j].StatementID
		}
		return claims[i].PropertySlug < claims[j].PropertySlug
	})
	return claims
}

// extractCitations deduplicates and sorts sources from the bundle.
func extractCitations(b *assembler.Bundle) []citation {
	var cites []citation
	seen := make(map[string]bool)
	for _, src := range b.Sources {
		if seen[src.ID] {
			continue
		}
		seen[src.ID] = true
		cites = append(cites, citation{
			SourceID:           src.ID,
			URL:                src.URL,
			VerificationStatus: src.VerificationStatus,
		})
	}
	sort.Slice(cites, func(i, j int) bool {
		return cites[i].SourceID < cites[j].SourceID
	})
	return cites
}

// detectConflicts finds pairs of statements on the same entity+property with differing values.
func detectConflicts(b *assembler.Bundle) []conflict {
	type key struct{ EntityID, PropertySlug string }
	byKey := make(map[key][]assembler.BundleStatement)
	for _, s := range b.Statements {
		k := key{s.EntityID, s.PropertySlug}
		byKey[k] = append(byKey[k], s)
	}

	var conflicts []conflict
	for _, stmts := range byKey {
		if len(stmts) < 2 {
			continue
		}
		// Sort so comparison is order-independent and deterministic.
		sort.Slice(stmts, func(i, j int) bool {
			return stmts[i].ID < stmts[j].ID
		})
		for i := 0; i < len(stmts)-1; i++ {
			for j := i + 1; j < len(stmts); j++ {
				if stmts[i].Value != stmts[j].Value {
					conflicts = append(conflicts, conflict{
						EntityID:     stmts[i].EntityID,
						PropertySlug: stmts[i].PropertySlug,
						ValueA:       stmts[i].Value,
						ValueB:       stmts[j].Value,
					})
				}
			}
		}
	}
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].EntityID != conflicts[j].EntityID {
			return conflicts[i].EntityID < conflicts[j].EntityID
		}
		if conflicts[i].PropertySlug != conflicts[j].PropertySlug {
			return conflicts[i].PropertySlug < conflicts[j].PropertySlug
		}
		return conflicts[i].ValueA < conflicts[j].ValueA
	})
	return conflicts
}

// buildSourceHealth returns a sorted status summary for each source.
func buildSourceHealth(b *assembler.Bundle) []sourceEntry {
	var entries []sourceEntry
	seen := make(map[string]bool)
	for _, src := range b.Sources {
		if seen[src.ID] {
			continue
		}
		seen[src.ID] = true
		entries = append(entries, sourceEntry{
			SourceID: src.ID,
			Status:   src.VerificationStatus,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SourceID < entries[j].SourceID
	})
	return entries
}

// findEvidenceGaps returns statements that have no supporting sources.
func findEvidenceGaps(b *assembler.Bundle) []evidenceGap {
	var gaps []evidenceGap
	for _, s := range b.Statements {
		if len(s.SourceIDs) == 0 {
			gaps = append(gaps, evidenceGap{
				StatementID:  s.ID,
				PropertySlug: s.PropertySlug,
			})
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		return gaps[i].StatementID < gaps[j].StatementID
	})
	return gaps
}

// deriveWriterPrompts builds a small deterministic set of narrative prompts.
func deriveWriterPrompts(b *assembler.Bundle) []string {
	prompts := []string{
		fmt.Sprintf("Summarize the evidence for entity %s based on the assembled claims.", b.EntityID),
	}

	// Add conflict-driven prompt if conflicts exist.
	conflicts := detectConflicts(b)
	if len(conflicts) > 0 {
		c := conflicts[0]
		prompts = append(prompts,
			fmt.Sprintf("Investigate the conflicting values for %q on entity %s: %q vs %q.",
				c.PropertySlug, c.EntityID, c.ValueA, c.ValueB),
		)
	}

	// Add gap-driven prompt if evidence gaps exist.
	gaps := findEvidenceGaps(b)
	if len(gaps) > 0 {
		prompts = append(prompts,
			fmt.Sprintf("Find sources to fill the evidence gap for property %q on statement %s.",
				gaps[0].PropertySlug, gaps[0].StatementID),
		)
	}

	sort.Strings(prompts)
	return prompts
}

// Service provides tenant-scoped operations on evidence_briefs.
type Service struct {
	Pool *pgxpool.Pool
}

// tenantTx begins a transaction with the app.tenant_id session variable set
// so that RLS policies on evidence_briefs allow access only to the given tenant.
func (s Service) tenantTx(ctx context.Context, tenantID string) (pgx.Tx, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, fmt.Errorf("briefs: begin tx: %w", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.tenant_id', $1, true)", tenantID); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return nil, errors.Join(fmt.Errorf("briefs: set tenant_id: %w", err), fmt.Errorf("briefs: rollback: %w", rollbackErr))
		}
		return nil, fmt.Errorf("briefs: set tenant_id: %w", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE app_user"); err != nil {
		if rollbackErr := tx.Rollback(ctx); rollbackErr != nil {
			return nil, errors.Join(fmt.Errorf("briefs: set role: %w", err), fmt.Errorf("briefs: rollback: %w", rollbackErr))
		}
		return nil, fmt.Errorf("briefs: set role: %w", err)
	}
	return tx, nil
}

// GenerateAndStore generates a brief from req.Bundle and inserts it into evidence_briefs.
// Returns the stored Brief record with its database-assigned ID.
func (s Service) GenerateAndStore(ctx context.Context, tenantID string, req GenerateRequest) (Brief, error) {
	g := BriefGenerator{}
	payload, err := g.Generate(req.Bundle)
	if err != nil {
		return Brief{}, fmt.Errorf("briefs: generate: %w", err)
	}

	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return Brief{}, err
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "rollback after commit: %v\n", err)
		}
	}()

	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO evidence_briefs (tenant_id, source_kind, entity_id, query, payload)
		VALUES ($1::uuid, $2, $3::uuid, $4, $5)
		RETURNING id::text
	`, tenantID, string(req.SourceKind), req.EntityID, req.Query, payload).Scan(&id)
	if err != nil {
		return Brief{}, fmt.Errorf("briefs: insert: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Brief{}, fmt.Errorf("briefs: commit: %w", err)
	}

	return Brief{
		ID:         id,
		SourceKind: req.SourceKind,
		EntityID:   req.EntityID,
		Payload:    payload,
	}, nil
}

// List returns briefs for tenantID, applying optional SourceKind and EntityID filters.
// Results are ordered by created_at DESC, limited to opts.Limit rows.
func (s Service) List(ctx context.Context, tenantID string, opts ListOptions) ([]Brief, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "rollback after commit: %v\n", err)
		}
	}()

	query := `
		SELECT id::text, source_kind, entity_id::text, payload
		FROM evidence_briefs
		WHERE tenant_id = $1::uuid
	`
	args := []any{tenantID}
	argIdx := 2

	if opts.SourceKind != nil {
		query += fmt.Sprintf(" AND source_kind = $%d", argIdx)
		args = append(args, string(*opts.SourceKind))
		argIdx++
	}
	if opts.EntityID != nil {
		query += fmt.Sprintf(" AND entity_id = $%d::uuid", argIdx)
		args = append(args, *opts.EntityID)
		argIdx++
	}
	if opts.CreatedAfter != nil {
		query += fmt.Sprintf(" AND created_at >= $%d::timestamptz", argIdx)
		args = append(args, *opts.CreatedAfter)
		argIdx++
	}
	if opts.CreatedBefore != nil {
		query += fmt.Sprintf(" AND created_at <= $%d::timestamptz", argIdx)
		args = append(args, *opts.CreatedBefore)
		argIdx++
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", argIdx)
	args = append(args, opts.Limit)

	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("briefs: list query: %w", err)
	}
	defer rows.Close()

	var results []Brief
	for rows.Next() {
		var b Brief
		var entityID *string
		var rawPayload []byte
		if err := rows.Scan(&b.ID, &b.SourceKind, &entityID, &rawPayload); err != nil {
			return nil, fmt.Errorf("briefs: list scan: %w", err)
		}
		b.EntityID = entityID
		b.Payload = rawPayload
		results = append(results, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("briefs: list rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("briefs: list commit: %w", err)
	}
	return results, nil
}

// Get retrieves a single brief by ID under tenantID. Cross-tenant access returns an error
// because RLS filters the row out, resulting in pgx.ErrNoRows (wrapped).
func (s Service) Get(ctx context.Context, tenantID, id string) (Brief, error) {
	tx, err := s.tenantTx(ctx, tenantID)
	if err != nil {
		return Brief{}, err
	}
	defer func() {
		if err := tx.Rollback(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "rollback after commit: %v\n", err)
		}
	}()

	var b Brief
	var entityID *string
	var rawPayload []byte
	err = tx.QueryRow(ctx, `
		SELECT id::text, source_kind, entity_id::text, payload
		FROM evidence_briefs
		WHERE tenant_id = $1::uuid AND id = $2::uuid
	`, tenantID, id).Scan(&b.ID, &b.SourceKind, &entityID, &rawPayload)
	if err != nil {
		return Brief{}, fmt.Errorf("briefs: get: %w", err)
	}
	b.EntityID = entityID
	b.Payload = rawPayload

	if err := tx.Commit(ctx); err != nil {
		return Brief{}, fmt.Errorf("briefs: get commit: %w", err)
	}
	return b, nil
}
