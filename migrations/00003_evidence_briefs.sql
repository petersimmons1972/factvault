-- +goose Up
-- +goose StatementBegin
CREATE TABLE evidence_briefs (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id   UUID NOT NULL,
    source_kind TEXT NOT NULL CHECK (source_kind IN ('dossier', 'story')),
    entity_id   UUID REFERENCES entities(id),
    query       TEXT,
    payload     JSONB NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_evidence_briefs_tenant ON evidence_briefs (tenant_id, created_at DESC);
CREATE INDEX idx_evidence_briefs_entity ON evidence_briefs (tenant_id, entity_id) WHERE entity_id IS NOT NULL;
CREATE INDEX idx_evidence_briefs_kind   ON evidence_briefs (tenant_id, source_kind);

GRANT SELECT, INSERT, UPDATE, DELETE ON evidence_briefs TO app_user;

ALTER TABLE evidence_briefs ENABLE ROW LEVEL SECURITY;
ALTER TABLE evidence_briefs FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON evidence_briefs
    USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS evidence_briefs;
-- +goose StatementEnd
