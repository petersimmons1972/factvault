-- This file is reference documentation only.
-- Migrations in factvault/db/migrations/versions/ are the source of truth.
-- Keep this file in sync manually after schema changes.
--
-- Dependency order: pgvector → entities/properties → statements/qualifiers →
--   relations → sources → statement_sources → source_verifications →
--   proposed_properties → dossiers → (embedding columns) → (HNSW indices) →
--   (RLS) → v_conflicts

-- ---------------------------------------------------------------------------
-- 0001: pgvector extension
-- ---------------------------------------------------------------------------
CREATE EXTENSION IF NOT EXISTS vector;

-- ---------------------------------------------------------------------------
-- 0002: entities and properties
-- ---------------------------------------------------------------------------
CREATE TABLE entities (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    ext_id      TEXT,
    label       TEXT NOT NULL,
    type_uri    TEXT,
    description TEXT,
    embedding   vector(1024),
    meta        JSONB NOT NULL DEFAULT '{}',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, ext_id) NULLS NOT DISTINCT
);

CREATE INDEX idx_entities_tenant    ON entities (tenant_id);
CREATE INDEX idx_entities_label     ON entities (tenant_id, lower(label));
CREATE INDEX idx_entities_type      ON entities (tenant_id, type_uri);

CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    slug        TEXT NOT NULL,
    label       TEXT NOT NULL,
    value_type  TEXT NOT NULL
                CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    UNIQUE (tenant_id, slug) NULLS NOT DISTINCT
);

-- ---------------------------------------------------------------------------
-- 0003: statements and qualifiers
-- ---------------------------------------------------------------------------
CREATE TABLE statements (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    subject_id   UUID NOT NULL REFERENCES entities(id),
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_entity   UUID REFERENCES entities(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_json     JSONB,
    rank         TEXT NOT NULL DEFAULT 'normal'
                 CHECK (rank IN ('preferred', 'normal', 'deprecated')),
    confidence   NUMERIC(4,3) NOT NULL
                 CHECK (confidence >= 0 AND confidence <= 1),
    embedding    vector(1024),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_statement_value_populated
        CHECK (
            (val_entity IS NOT NULL)::int +
            (val_text   IS NOT NULL)::int +
            (val_number IS NOT NULL)::int +
            (val_date   IS NOT NULL)::int = 1
        )
);

CREATE INDEX idx_statements_subject    ON statements (subject_id, property_id, rank);
CREATE INDEX idx_statements_tenant     ON statements (tenant_id, subject_id);
CREATE INDEX idx_statements_val_entity ON statements (val_entity) WHERE val_entity IS NOT NULL;
CREATE INDEX idx_statements_confidence ON statements (confidence DESC);

CREATE TABLE qualifiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    property_id  UUID NOT NULL REFERENCES properties(id),
    val_text     TEXT,
    val_number   NUMERIC,
    val_date     TIMESTAMPTZ,
    val_entity   UUID REFERENCES entities(id),
    CONSTRAINT chk_qualifier_value_populated
        CHECK (
            (val_entity IS NOT NULL)::int +
            (val_text   IS NOT NULL)::int +
            (val_number IS NOT NULL)::int +
            (val_date   IS NOT NULL)::int = 1
        )
);

CREATE INDEX idx_qualifiers_statement ON qualifiers (statement_id);

-- ---------------------------------------------------------------------------
-- 0004: relations
-- ---------------------------------------------------------------------------
CREATE TABLE relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    source_id    UUID NOT NULL REFERENCES entities(id),
    target_id    UUID NOT NULL REFERENCES entities(id),
    type         TEXT NOT NULL,
    weight       NUMERIC,
    confidence   NUMERIC(4,3),
    description  TEXT,
    embedding    vector(1024),
    meta         JSONB NOT NULL DEFAULT '{}',
    statement_id UUID REFERENCES statements(id) ON DELETE CASCADE,
    UNIQUE (tenant_id, source_id, target_id, type)
);

CREATE INDEX idx_relations_source    ON relations (tenant_id, source_id);
CREATE INDEX idx_relations_target    ON relations (tenant_id, target_id);
CREATE INDEX idx_relations_type      ON relations (tenant_id, type);

-- ---------------------------------------------------------------------------
-- 0005: sources
-- ---------------------------------------------------------------------------
CREATE TABLE sources (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id        UUID NOT NULL,
    url              TEXT NOT NULL,
    fetched_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    content_hash     TEXT NOT NULL,
    raw_html         BYTEA,
    raw_text         TEXT,
    archive_url      TEXT,
    publisher        TEXT,
    title            TEXT,
    published_at     TIMESTAMPTZ,
    embedding        vector(1024),
    last_verified_at TIMESTAMPTZ,
    status           TEXT NOT NULL DEFAULT 'collected'
                     CHECK (status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')),
    UNIQUE (tenant_id, url)
);

CREATE INDEX idx_sources_tenant_status ON sources (tenant_id, status);
CREATE INDEX idx_sources_last_verified ON sources (last_verified_at);
CREATE INDEX idx_sources_published_at  ON sources (published_at);

-- ---------------------------------------------------------------------------
-- 0006: statement_sources
-- ---------------------------------------------------------------------------
CREATE TABLE statement_sources (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id         UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id            UUID NOT NULL REFERENCES sources(id),
    tenant_id            UUID NOT NULL,
    excerpt              TEXT NOT NULL,
    excerpt_offset_start INTEGER NOT NULL,
    excerpt_offset_end   INTEGER NOT NULL,
    extraction_method    TEXT NOT NULL,
    extracted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence           NUMERIC(4,3),
    UNIQUE (statement_id, source_id)
);

CREATE INDEX idx_stmt_sources_statement ON statement_sources (statement_id);
CREATE INDEX idx_stmt_sources_source    ON statement_sources (source_id);

-- ---------------------------------------------------------------------------
-- 0007: source_verifications
-- ---------------------------------------------------------------------------
CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL
                     CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
    new_content_hash TEXT,
    notes            TEXT
);

CREATE INDEX idx_source_verifications_source ON source_verifications (source_id, verified_at DESC);
CREATE INDEX idx_source_verifications_status ON source_verifications (status, verified_at DESC);

CREATE OR REPLACE FUNCTION deny_source_verifications_mutation()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'source_verifications is append-only. DELETE and UPDATE are forbidden.';
END;
$$;

CREATE TRIGGER trg_source_verifications_no_update
    BEFORE UPDATE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();

CREATE TRIGGER trg_source_verifications_no_delete
    BEFORE DELETE ON source_verifications
    FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation();

-- ---------------------------------------------------------------------------
-- 0008: proposed_properties
-- ---------------------------------------------------------------------------
CREATE TABLE proposed_properties (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    proposed_slug       TEXT NOT NULL,
    proposed_value_type TEXT NOT NULL
                        CHECK (proposed_value_type IN
                            ('entity_ref', 'string', 'number', 'date', 'url')),
    proposed_by         TEXT NOT NULL,
    example_excerpt     TEXT,
    example_source_id   UUID REFERENCES sources(id),
    status              TEXT NOT NULL DEFAULT 'pending'
                        CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    UNIQUE (tenant_id, proposed_slug, status)
);

CREATE INDEX idx_proposed_properties_tenant_status ON proposed_properties (tenant_id, status);

-- ---------------------------------------------------------------------------
-- 0009: dossiers cache
-- ---------------------------------------------------------------------------
CREATE TABLE dossiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    entity_id    UUID NOT NULL REFERENCES entities(id),
    assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bundle       JSONB NOT NULL,
    UNIQUE (tenant_id, entity_id)
);

CREATE INDEX idx_dossiers_tenant_assembled ON dossiers (tenant_id, assembled_at DESC);

-- ---------------------------------------------------------------------------
-- 0010: embedding columns (added via ALTER TABLE in migration; shown here for completeness)
-- NOTE: Already included inline above in each table definition.
-- ---------------------------------------------------------------------------

-- ---------------------------------------------------------------------------
-- 0011: HNSW indices on embedding columns
-- NOTE: Migration uses CREATE INDEX CONCURRENTLY to avoid exclusive table locks.
-- CONCURRENTLY requires autocommit mode (outside a transaction block).
-- ---------------------------------------------------------------------------
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_entities_embedding   ON entities   USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_statements_embedding ON statements USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_relations_embedding  ON relations  USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sources_embedding    ON sources    USING hnsw (embedding vector_cosine_ops) WITH (m = 16, ef_construction = 64);

-- ---------------------------------------------------------------------------
-- 0012: RLS policies
--
-- NOTE: All policies use NULLIF(current_setting('app.tenant_id', true), '')::uuid
-- rather than the simpler current_setting('app.tenant_id', true)::uuid.
-- Rationale: current_setting with missing_ok=true returns '' (empty string)
-- when the GUC is unset, not NULL. Casting '' to uuid raises
-- InvalidTextRepresentation. NULLIF converts '' to NULL, and NULL::uuid is NULL,
-- so tenant_id = NULL evaluates to NULL (row is filtered out, not errored).
-- ---------------------------------------------------------------------------
ALTER TABLE entities              ENABLE ROW LEVEL SECURITY;
ALTER TABLE entities              FORCE ROW LEVEL SECURITY;
ALTER TABLE properties            ENABLE ROW LEVEL SECURITY;
ALTER TABLE properties            FORCE ROW LEVEL SECURITY;
ALTER TABLE statements            ENABLE ROW LEVEL SECURITY;
ALTER TABLE statements            FORCE ROW LEVEL SECURITY;
ALTER TABLE qualifiers            ENABLE ROW LEVEL SECURITY;
ALTER TABLE qualifiers            FORCE ROW LEVEL SECURITY;
ALTER TABLE relations             ENABLE ROW LEVEL SECURITY;
ALTER TABLE relations             FORCE ROW LEVEL SECURITY;
ALTER TABLE sources               ENABLE ROW LEVEL SECURITY;
ALTER TABLE sources               FORCE ROW LEVEL SECURITY;
ALTER TABLE statement_sources     ENABLE ROW LEVEL SECURITY;
ALTER TABLE statement_sources     FORCE ROW LEVEL SECURITY;
ALTER TABLE source_verifications  ENABLE ROW LEVEL SECURITY;
ALTER TABLE source_verifications  FORCE ROW LEVEL SECURITY;
ALTER TABLE proposed_properties   ENABLE ROW LEVEL SECURITY;
ALTER TABLE proposed_properties   FORCE ROW LEVEL SECURITY;
ALTER TABLE dossiers              ENABLE ROW LEVEL SECURITY;
ALTER TABLE dossiers              FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_isolation ON entities
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON properties
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON statements
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON qualifiers
    USING (
        EXISTS (
            SELECT 1 FROM statements s
            WHERE s.id = qualifiers.statement_id
              AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
        )
    );
CREATE POLICY tenant_isolation ON relations
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON sources
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON statement_sources
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON source_verifications
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON proposed_properties
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
CREATE POLICY tenant_isolation ON dossiers
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- ---------------------------------------------------------------------------
-- 0013: v_conflicts view
-- ---------------------------------------------------------------------------
CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    subject_id,
    property_id,
    tenant_id,
    COUNT(*) AS competing_count,
    array_agg(id) AS statement_ids
FROM statements
WHERE rank != 'deprecated'
GROUP BY subject_id, property_id, tenant_id
HAVING COUNT(
    DISTINCT COALESCE(
        val_entity::text,
        val_text,
        val_number::text,
        val_date::text,
        val_json::text
    )
) > 1;
