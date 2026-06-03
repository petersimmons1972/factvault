-- +goose Up
-- +goose StatementBegin
ALTER TABLE evidence_briefs ADD COLUMN IF NOT EXISTS bundle_hash TEXT;
CREATE INDEX IF NOT EXISTS evidence_briefs_bundle_hash_idx ON evidence_briefs(bundle_hash) WHERE bundle_hash IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS evidence_briefs_bundle_hash_idx;
ALTER TABLE evidence_briefs DROP COLUMN IF EXISTS bundle_hash;
-- +goose StatementEnd
