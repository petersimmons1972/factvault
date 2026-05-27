//go:build sqlite && cgo

package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type migration struct {
	version int
	name    string
	sql     string
}

var migrations = []migration{
	{
		version: 1,
		name:    "initial_schema",
		sql: `
CREATE TABLE IF NOT EXISTS entities (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    tenant_id   TEXT NOT NULL,
    ext_id      TEXT,
    label       TEXT NOT NULL,
    type_uri    TEXT,
    description TEXT,
    embedding   BLOB,
    meta        BLOB NOT NULL DEFAULT (json_object()),
    created_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (tenant_id, ext_id)
);
CREATE INDEX IF NOT EXISTS idx_entities_tenant ON entities (tenant_id);
CREATE INDEX IF NOT EXISTS idx_entities_label ON entities (tenant_id, lower(label));
CREATE INDEX IF NOT EXISTS idx_entities_type ON entities (tenant_id, type_uri);

CREATE TABLE IF NOT EXISTS properties (
    id          TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    tenant_id   TEXT,
    slug        TEXT NOT NULL,
    label       TEXT NOT NULL,
    value_type  TEXT NOT NULL CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    UNIQUE (tenant_id, slug)
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_properties_global_slug ON properties (slug) WHERE tenant_id IS NULL;

CREATE TABLE IF NOT EXISTS statements (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    tenant_id    TEXT NOT NULL,
    subject_id   TEXT NOT NULL REFERENCES entities(id),
    property_id  TEXT NOT NULL REFERENCES properties(id),
    val_entity   TEXT REFERENCES entities(id),
    val_text     TEXT,
    val_number   TEXT,
    val_date     TEXT,
    val_json     BLOB,
    rank         TEXT NOT NULL DEFAULT 'normal' CHECK (rank IN ('preferred', 'normal', 'deprecated')),
    confidence   TEXT NOT NULL CHECK (CAST(confidence AS REAL) >= 0 AND CAST(confidence AS REAL) <= 1),
    embedding    BLOB,
    created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    CHECK ((val_entity IS NOT NULL) + (val_text IS NOT NULL) + (val_number IS NOT NULL) + (val_date IS NOT NULL) = 1)
);
CREATE INDEX IF NOT EXISTS idx_statements_subject ON statements (subject_id, property_id, rank);
CREATE INDEX IF NOT EXISTS idx_statements_tenant ON statements (tenant_id, subject_id);
CREATE INDEX IF NOT EXISTS idx_statements_val_entity ON statements (val_entity) WHERE val_entity IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_statements_confidence ON statements (confidence DESC);

CREATE TABLE IF NOT EXISTS qualifiers (
    id           TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    statement_id TEXT NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    property_id  TEXT NOT NULL REFERENCES properties(id),
    val_text     TEXT,
    val_number   TEXT,
    val_date     TEXT,
    val_entity   TEXT REFERENCES entities(id),
    CHECK ((val_entity IS NOT NULL) + (val_text IS NOT NULL) + (val_number IS NOT NULL) + (val_date IS NOT NULL) = 1)
);
CREATE INDEX IF NOT EXISTS idx_qualifiers_statement ON qualifiers (statement_id);

CREATE TABLE IF NOT EXISTS sources (
    id               TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    tenant_id        TEXT NOT NULL,
    url              TEXT NOT NULL,
    fetched_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    content_hash     TEXT NOT NULL,
    raw_html         BLOB,
    raw_text         TEXT,
    archive_url      TEXT,
    publisher        TEXT,
    title            TEXT,
    published_at     TEXT,
    embedding        BLOB,
    last_verified_at TEXT,
    status           TEXT NOT NULL DEFAULT 'collected' CHECK (status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')),
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    UNIQUE (tenant_id, url)
);
CREATE INDEX IF NOT EXISTS idx_sources_tenant_status ON sources (tenant_id, status);
CREATE INDEX IF NOT EXISTS idx_sources_last_verified ON sources (last_verified_at);
CREATE INDEX IF NOT EXISTS idx_sources_published_at ON sources (published_at);

CREATE TABLE IF NOT EXISTS statement_sources (
    id                   TEXT PRIMARY KEY DEFAULT (lower(hex(randomblob(4))) || '-' || lower(hex(randomblob(2))) || '-4' || substr(lower(hex(randomblob(2))), 2) || '-' || substr('89ab', abs(random()) % 4 + 1, 1) || substr(lower(hex(randomblob(2))), 2) || '-' || lower(hex(randomblob(6)))),
    statement_id         TEXT NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id            TEXT NOT NULL REFERENCES sources(id),
    excerpt              TEXT NOT NULL,
    excerpt_offset_start INTEGER NOT NULL CHECK (excerpt_offset_start >= 0),
    excerpt_offset_end   INTEGER NOT NULL CHECK (excerpt_offset_end > excerpt_offset_start),
    extraction_method    TEXT NOT NULL,
    extracted_at         TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    confidence           TEXT CHECK (confidence IS NULL OR (CAST(confidence AS REAL) >= 0 AND CAST(confidence AS REAL) <= 1)),
    tenant_id            TEXT NOT NULL,
    UNIQUE (statement_id, source_id)
);
CREATE INDEX IF NOT EXISTS idx_stmt_sources_statement ON statement_sources (statement_id);
CREATE INDEX IF NOT EXISTS idx_stmt_sources_source ON statement_sources (source_id);
`,
	},
	{
		version: 2,
		name:    "sqlite_vec_probe",
		sql: `
CREATE VIRTUAL TABLE IF NOT EXISTS entity_embedding_vec USING vec0(embedding float[1024]);
`,
	},
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		return fmt.Errorf("enable sqlite foreign keys: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `SELECT vec_version()`); err != nil {
		return fmt.Errorf("load sqlite-vec: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')))`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}
	for _, migration := range migrations {
		applied, err := migrationApplied(ctx, s.db, migration.version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		if err := applyMigration(ctx, s.db, migration); err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, db *sql.DB, version int) (bool, error) {
	var exists int
	err := db.QueryRowContext(ctx, `SELECT 1 FROM schema_migrations WHERE version = ?`, version).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("check sqlite migration %d: %w", version, err)
}

func applyMigration(ctx context.Context, db *sql.DB, migration migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin sqlite migration %d: %w", migration.version, err)
	}
	defer rollbackTx(tx)
	if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
		return fmt.Errorf("apply sqlite migration %d %s: %w", migration.version, migration.name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`, migration.version, migration.name); err != nil {
		return fmt.Errorf("record sqlite migration %d: %w", migration.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit sqlite migration %d: %w", migration.version, err)
	}
	return nil
}

func rollbackTx(tx *sql.Tx) {
	if err := tx.Rollback(); err != nil {
		return
	}
}
