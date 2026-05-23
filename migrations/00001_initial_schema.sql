-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'app_user') THEN
        CREATE ROLE app_user WITH LOGIN PASSWORD 'changeme_in_production';
    END IF;
END
$$;

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
    CONSTRAINT uq_entities_tenant_ext_id UNIQUE NULLS NOT DISTINCT (tenant_id, ext_id)
);
CREATE INDEX idx_entities_tenant ON entities (tenant_id);
CREATE INDEX idx_entities_label ON entities (tenant_id, lower(label));
CREATE INDEX idx_entities_type ON entities (tenant_id, type_uri);

CREATE TABLE properties (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID,
    slug        TEXT NOT NULL,
    label       TEXT NOT NULL,
    value_type  TEXT NOT NULL CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    description TEXT,
    CONSTRAINT uq_properties_tenant_slug UNIQUE NULLS NOT DISTINCT (tenant_id, slug)
);

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
    rank         TEXT NOT NULL DEFAULT 'normal' CHECK (rank IN ('preferred', 'normal', 'deprecated')),
    confidence   NUMERIC(4,3) NOT NULL CHECK (confidence >= 0 AND confidence <= 1),
    embedding    vector(1024),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT chk_statement_value_populated CHECK (
        (val_entity IS NOT NULL)::int +
        (val_text IS NOT NULL)::int +
        (val_number IS NOT NULL)::int +
        (val_date IS NOT NULL)::int = 1
    )
);
CREATE INDEX idx_statements_subject ON statements (subject_id, property_id, rank);
CREATE INDEX idx_statements_tenant ON statements (tenant_id, subject_id);
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
    CONSTRAINT chk_qualifier_value_populated CHECK (
        (val_entity IS NOT NULL)::int +
        (val_text IS NOT NULL)::int +
        (val_number IS NOT NULL)::int +
        (val_date IS NOT NULL)::int = 1
    )
);
CREATE INDEX idx_qualifiers_statement ON qualifiers (statement_id);

CREATE TABLE relations (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    source_id    UUID NOT NULL REFERENCES entities(id),
    target_id    UUID NOT NULL REFERENCES entities(id),
    type         TEXT NOT NULL,
    weight       NUMERIC,
    confidence   NUMERIC(4,3) CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    description  TEXT,
    embedding    vector(1024),
    meta         JSONB NOT NULL DEFAULT '{}',
    statement_id UUID REFERENCES statements(id) ON DELETE CASCADE,
    CONSTRAINT uq_relations_tenant_source_target_type UNIQUE (tenant_id, source_id, target_id, type)
);
CREATE INDEX idx_relations_source ON relations (tenant_id, source_id);
CREATE INDEX idx_relations_target ON relations (tenant_id, target_id);
CREATE INDEX idx_relations_type ON relations (tenant_id, type);

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
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT uq_sources_tenant_url UNIQUE (tenant_id, url)
);
CREATE INDEX idx_sources_tenant_status ON sources (tenant_id, status);
CREATE INDEX idx_sources_last_verified ON sources (last_verified_at);
CREATE INDEX idx_sources_published_at ON sources (published_at);

CREATE TABLE statement_sources (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    statement_id         UUID NOT NULL REFERENCES statements(id) ON DELETE CASCADE,
    source_id            UUID NOT NULL REFERENCES sources(id),
    excerpt              TEXT NOT NULL,
    excerpt_offset_start INTEGER NOT NULL CHECK (excerpt_offset_start >= 0),
    excerpt_offset_end   INTEGER NOT NULL CHECK (excerpt_offset_end > excerpt_offset_start),
    extraction_method    TEXT NOT NULL,
    extracted_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    confidence           NUMERIC(4,3) CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
    tenant_id            UUID NOT NULL,
    CONSTRAINT uq_statement_sources_stmt_src UNIQUE (statement_id, source_id)
);
CREATE INDEX idx_stmt_sources_statement ON statement_sources (statement_id);
CREATE INDEX idx_stmt_sources_source ON statement_sources (source_id);

CREATE TABLE source_verifications (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source_id        UUID NOT NULL REFERENCES sources(id),
    tenant_id        UUID NOT NULL,
    verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    status           TEXT NOT NULL CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
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

CREATE TABLE proposed_properties (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,
    proposed_slug       TEXT NOT NULL,
    proposed_value_type TEXT NOT NULL CHECK (proposed_value_type IN ('entity_ref', 'string', 'number', 'date', 'url')),
    proposed_by         TEXT NOT NULL,
    example_excerpt     TEXT,
    example_source_id   UUID REFERENCES sources(id),
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'rejected')),
    reviewed_by         TEXT,
    reviewed_at         TIMESTAMPTZ,
    created_at          TIMESTAMPTZ DEFAULT now(),
    CONSTRAINT uq_proposed_properties_tenant_slug_status UNIQUE (tenant_id, proposed_slug, status)
);
CREATE INDEX idx_proposed_properties_tenant_status ON proposed_properties (tenant_id, status);

CREATE TABLE dossiers (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    entity_id    UUID NOT NULL REFERENCES entities(id),
    assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    bundle       JSONB NOT NULL,
    CONSTRAINT uq_dossiers_tenant_entity UNIQUE (tenant_id, entity_id)
);
CREATE INDEX idx_dossiers_tenant_assembled ON dossiers (tenant_id, assembled_at DESC);

REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO app_user;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO app_user;

DO $$
DECLARE
    t TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY[
        'entities','properties','statements','relations','sources',
        'statement_sources','source_verifications','proposed_properties','dossiers'
    ] LOOP
        EXECUTE format('ALTER TABLE %I ENABLE ROW LEVEL SECURITY', t);
        EXECUTE format('ALTER TABLE %I FORCE ROW LEVEL SECURITY', t);
        EXECUTE format(
            'CREATE POLICY tenant_isolation ON %I USING (tenant_id = NULLIF(current_setting(''app.tenant_id'', true), '''')::uuid)',
            t
        );
    END LOOP;
END
$$;

CREATE POLICY tenant_isolation ON qualifiers
USING (
    EXISTS (
        SELECT 1 FROM statements s
        WHERE s.id = qualifiers.statement_id
          AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    )
);
ALTER TABLE qualifiers ENABLE ROW LEVEL SECURITY;
ALTER TABLE qualifiers FORCE ROW LEVEL SECURITY;

CREATE OR REPLACE VIEW v_conflicts AS
SELECT
    s1.id AS statement_a_id,
    s2.id AS statement_b_id,
    s1.tenant_id,
    s1.subject_id,
    s1.property_id,
    p.slug AS property_slug,
    s1.val_text AS val_a_text,
    s1.val_number AS val_a_number,
    s1.val_date AS val_a_date,
    s1.val_entity AS val_a_entity,
    s2.val_text AS val_b_text,
    s2.val_number AS val_b_number,
    s2.val_date AS val_b_date,
    s2.val_entity AS val_b_entity,
    s1.confidence AS confidence_a,
    s2.confidence AS confidence_b,
    s1.rank AS rank_a,
    s2.rank AS rank_b,
    s1.created_at AS created_a,
    s2.created_at AS created_b
FROM statements s1
JOIN statements s2
    ON s1.subject_id = s2.subject_id
   AND s1.property_id = s2.property_id
   AND s1.id < s2.id
   AND s1.rank != 'deprecated'
   AND s2.rank != 'deprecated'
JOIN properties p ON p.id = s1.property_id
WHERE
    (s1.val_text IS DISTINCT FROM s2.val_text) OR
    (s1.val_number IS DISTINCT FROM s2.val_number) OR
    (s1.val_date IS DISTINCT FROM s2.val_date) OR
    (s1.val_entity IS DISTINCT FROM s2.val_entity);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP VIEW IF EXISTS v_conflicts;
DROP TRIGGER IF EXISTS trg_source_verifications_no_delete ON source_verifications;
DROP TRIGGER IF EXISTS trg_source_verifications_no_update ON source_verifications;
DROP FUNCTION IF EXISTS deny_source_verifications_mutation();
DROP TABLE IF EXISTS dossiers;
DROP TABLE IF EXISTS proposed_properties;
DROP TABLE IF EXISTS source_verifications;
DROP TABLE IF EXISTS statement_sources;
DROP TABLE IF EXISTS sources;
DROP TABLE IF EXISTS relations;
DROP TABLE IF EXISTS qualifiers;
DROP TABLE IF EXISTS statements;
DROP TABLE IF EXISTS properties;
DROP TABLE IF EXISTS entities;
-- +goose StatementEnd
