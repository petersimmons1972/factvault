package workers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/petersimmons1972/factvault/internal/db"
	"github.com/petersimmons1972/factvault/internal/embed"
)

// EmbedWorker populates NULL embedding columns on entities, statements, and sources.
// It is idempotent: rows with an existing embedding are skipped.
// Every query runs under TenantContext / RLS, exactly like other workers.
type EmbedWorker struct {
	DB     *pgxpool.Pool
	Client *embed.Client
	Logger *slog.Logger
}

// EmbedOptions configures a single EmbedWorker run.
type EmbedOptions struct {
	// Limit caps how many rows are fetched per table per run.
	// Defaults to 100 if zero.
	Limit int
}

// EmbedResult reports what happened during a run.
type EmbedResult struct {
	// Populated is the total number of embedding columns written across all tables.
	Populated int
	// Skipped is the total number of rows that already had embeddings (not re-embedded).
	Skipped int
}

func (w *EmbedWorker) logger() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.New(slog.NewJSONHandler(os.Stderr, nil))
}

// RunOnce performs one backfill pass over entities, statements, and sources for the
// given tenant. It skips rows that already have embeddings (WHERE embedding IS NULL).
// The Limit option caps how many NULL rows are fetched per table.
func (w *EmbedWorker) RunOnce(ctx context.Context, tenantID string, opts EmbedOptions) (EmbedResult, error) {
	if w.DB == nil {
		return EmbedResult{}, fmt.Errorf("embed worker: nil db pool")
	}
	if tenantID == "" {
		return EmbedResult{}, fmt.Errorf("embed worker: tenant id required")
	}

	limit := opts.Limit
	if limit <= 0 {
		limit = 100
	}

	var tid pgtype.UUID
	if err := tid.Scan(tenantID); err != nil {
		return EmbedResult{}, fmt.Errorf("embed worker: invalid tenant id %q: %w", tenantID, err)
	}

	var total EmbedResult

	// Populate each table in a dedicated transaction so a failure in one table
	// does not roll back progress in others.
	entityResult, err := w.populateEntities(ctx, tid, limit)
	if err != nil {
		return total, fmt.Errorf("embed worker: entities: %w", err)
	}
	total.Populated += entityResult.Populated
	total.Skipped += entityResult.Skipped

	sourceResult, err := w.populateSources(ctx, tid, limit)
	if err != nil {
		return total, fmt.Errorf("embed worker: sources: %w", err)
	}
	total.Populated += sourceResult.Populated
	total.Skipped += sourceResult.Skipped

	stmtResult, err := w.populateStatements(ctx, tid, limit)
	if err != nil {
		return total, fmt.Errorf("embed worker: statements: %w", err)
	}
	total.Populated += stmtResult.Populated
	total.Skipped += stmtResult.Skipped

	w.logger().Info("embed worker: run complete",
		"tenant_id", tenantID,
		"populated", total.Populated,
		"skipped", total.Skipped,
	)
	return total, nil
}

// populateEntities fetches entities with NULL embeddings and embeds label + description.
func (w *EmbedWorker) populateEntities(ctx context.Context, tenantID pgtype.UUID, limit int) (EmbedResult, error) {
	txCtx := db.WithPool(ctx, w.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenantID)
	if err != nil {
		return EmbedResult{}, err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			fmt.Fprintf(os.Stderr, "embed worker: rollback entities: %v\n", err)
		}
	}()

	rows, err := tx.Query(txCtx, `
		SELECT id, label, description
		FROM entities
		WHERE tenant_id = $1 AND embedding IS NULL
		ORDER BY created_at ASC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("query entities: %w", err)
	}
	defer rows.Close()

	type row struct {
		id   pgtype.UUID
		text string
	}
	var pending []row
	for rows.Next() {
		var id pgtype.UUID
		var label string
		var description pgtype.Text
		if err := rows.Scan(&id, &label, &description); err != nil {
			return EmbedResult{}, fmt.Errorf("scan entity: %w", err)
		}
		text := entityEmbedText(label, description)
		pending = append(pending, row{id: id, text: text})
	}
	if err := rows.Err(); err != nil {
		return EmbedResult{}, fmt.Errorf("rows error: %w", err)
	}
	rows.Close()

	if len(pending) == 0 {
		return EmbedResult{}, nil
	}

	texts := make([]string, len(pending))
	for i, r := range pending {
		texts[i] = r.text
	}
	vecs, err := w.Client.Embed(txCtx, texts)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embed entities: %w", err)
	}
	if len(vecs) != len(pending) {
		return EmbedResult{}, fmt.Errorf("embed entities: expected %d vectors, got %d", len(pending), len(vecs))
	}

	for i, r := range pending {
		if _, err := tx.Exec(txCtx, `
			UPDATE entities SET embedding = $1 WHERE id = $2 AND embedding IS NULL
		`, pgvector.NewVector(vecs[i]), r.id); err != nil {
			return EmbedResult{}, fmt.Errorf("update entity %s: %w", r.id.String(), err)
		}
	}

	if err := tx.Commit(txCtx); err != nil {
		return EmbedResult{}, fmt.Errorf("commit entities: %w", err)
	}
	return EmbedResult{Populated: len(pending)}, nil
}

// populateSources fetches sources with NULL embeddings and embeds title + raw_text excerpt.
func (w *EmbedWorker) populateSources(ctx context.Context, tenantID pgtype.UUID, limit int) (EmbedResult, error) {
	txCtx := db.WithPool(ctx, w.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenantID)
	if err != nil {
		return EmbedResult{}, err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			fmt.Fprintf(os.Stderr, "embed worker: rollback sources: %v\n", err)
		}
	}()

	rows, err := tx.Query(txCtx, `
		SELECT id, title, raw_text
		FROM sources
		WHERE tenant_id = $1 AND embedding IS NULL
		ORDER BY created_at ASC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("query sources: %w", err)
	}
	defer rows.Close()

	type row struct {
		id   pgtype.UUID
		text string
	}
	var pending []row
	for rows.Next() {
		var id pgtype.UUID
		var title pgtype.Text
		var rawText pgtype.Text
		if err := rows.Scan(&id, &title, &rawText); err != nil {
			return EmbedResult{}, fmt.Errorf("scan source: %w", err)
		}
		text := sourceEmbedText(title, rawText)
		pending = append(pending, row{id: id, text: text})
	}
	if err := rows.Err(); err != nil {
		return EmbedResult{}, fmt.Errorf("rows error: %w", err)
	}
	rows.Close()

	if len(pending) == 0 {
		return EmbedResult{}, nil
	}

	texts := make([]string, len(pending))
	for i, r := range pending {
		texts[i] = r.text
	}
	vecs, err := w.Client.Embed(txCtx, texts)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embed sources: %w", err)
	}
	if len(vecs) != len(pending) {
		return EmbedResult{}, fmt.Errorf("embed sources: expected %d vectors, got %d", len(pending), len(vecs))
	}

	for i, r := range pending {
		if _, err := tx.Exec(txCtx, `
			UPDATE sources SET embedding = $1 WHERE id = $2 AND embedding IS NULL
		`, pgvector.NewVector(vecs[i]), r.id); err != nil {
			return EmbedResult{}, fmt.Errorf("update source %s: %w", r.id.String(), err)
		}
	}

	if err := tx.Commit(txCtx); err != nil {
		return EmbedResult{}, fmt.Errorf("commit sources: %w", err)
	}
	return EmbedResult{Populated: len(pending)}, nil
}

// populateStatements fetches statements with NULL embeddings and embeds a rendered text.
// Text = "subject property value" rendered from val_text (fallback to val_entity/val_number/val_date).
func (w *EmbedWorker) populateStatements(ctx context.Context, tenantID pgtype.UUID, limit int) (EmbedResult, error) {
	txCtx := db.WithPool(ctx, w.DB)
	txCtx, tx, err := db.TenantContext(txCtx, tenantID)
	if err != nil {
		return EmbedResult{}, err
	}
	defer func() {
		if err := tx.Rollback(txCtx); err != nil && !isRollbackAfterCommit(err) {
			fmt.Fprintf(os.Stderr, "embed worker: rollback statements: %v\n", err)
		}
	}()

	// Join to entities and properties to render a human-readable string.
	// Falls back gracefully if joins produce NULLs.
	rows, err := tx.Query(txCtx, `
		SELECT s.id,
		       COALESCE(e.label, ''),
		       COALESCE(p.slug, ''),
		       COALESCE(s.val_text, '')
		FROM statements s
		LEFT JOIN entities e ON e.id = s.subject_id
		LEFT JOIN properties p ON p.id = s.property_id
		WHERE s.tenant_id = $1 AND s.embedding IS NULL
		ORDER BY s.created_at ASC
		LIMIT $2
	`, tenantID, limit)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("query statements: %w", err)
	}
	defer rows.Close()

	type row struct {
		id   pgtype.UUID
		text string
	}
	var pending []row
	for rows.Next() {
		var id pgtype.UUID
		var subject, property, value string
		if err := rows.Scan(&id, &subject, &property, &value); err != nil {
			return EmbedResult{}, fmt.Errorf("scan statement: %w", err)
		}
		text := statementEmbedText(subject, property, value)
		pending = append(pending, row{id: id, text: text})
	}
	if err := rows.Err(); err != nil {
		return EmbedResult{}, fmt.Errorf("rows error: %w", err)
	}
	rows.Close()

	if len(pending) == 0 {
		return EmbedResult{}, nil
	}

	texts := make([]string, len(pending))
	for i, r := range pending {
		texts[i] = r.text
	}
	vecs, err := w.Client.Embed(txCtx, texts)
	if err != nil {
		return EmbedResult{}, fmt.Errorf("embed statements: %w", err)
	}
	if len(vecs) != len(pending) {
		return EmbedResult{}, fmt.Errorf("embed statements: expected %d vectors, got %d", len(pending), len(vecs))
	}

	for i, r := range pending {
		if _, err := tx.Exec(txCtx, `
			UPDATE statements SET embedding = $1 WHERE id = $2 AND embedding IS NULL
		`, pgvector.NewVector(vecs[i]), r.id); err != nil {
			return EmbedResult{}, fmt.Errorf("update statement %s: %w", r.id.String(), err)
		}
	}

	if err := tx.Commit(txCtx); err != nil {
		return EmbedResult{}, fmt.Errorf("commit statements: %w", err)
	}
	return EmbedResult{Populated: len(pending)}, nil
}

// entityEmbedText renders the text to embed for an entity.
// Uses label and description (if present), separated by a space.
func entityEmbedText(label string, description pgtype.Text) string {
	if description.Valid && strings.TrimSpace(description.String) != "" {
		return strings.TrimSpace(label) + " " + strings.TrimSpace(description.String)
	}
	return strings.TrimSpace(label)
}

// sourceEmbedText renders the text to embed for a source.
// Uses title and raw_text (truncated to 2048 chars to keep embedding requests reasonable).
func sourceEmbedText(title, rawText pgtype.Text) string {
	parts := make([]string, 0, 2)
	if title.Valid && strings.TrimSpace(title.String) != "" {
		parts = append(parts, strings.TrimSpace(title.String))
	}
	if rawText.Valid && strings.TrimSpace(rawText.String) != "" {
		text := strings.TrimSpace(rawText.String)
		if len(text) > 2048 {
			text = text[:2048]
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return "(no content)"
	}
	return strings.Join(parts, "\n")
}

// statementEmbedText renders the text to embed for a statement.
// Format: "subject property value"
func statementEmbedText(subject, property, value string) string {
	parts := make([]string, 0, 3)
	for _, s := range []string{subject, property, value} {
		s = strings.TrimSpace(s)
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "(empty statement)"
	}
	return strings.Join(parts, " ")
}

// isRollbackAfterCommit returns true for the benign "tx already closed" error
// that fires when a deferred Rollback runs after a successful Commit.
func isRollbackAfterCommit(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "tx is closed") || strings.Contains(msg, "already been closed")
}
