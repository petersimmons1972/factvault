-- +goose Up
-- +goose StatementBegin
ALTER TABLE sources ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE sources DROP COLUMN IF EXISTS meta;
-- +goose StatementEnd
