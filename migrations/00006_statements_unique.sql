-- +goose Up
-- +goose StatementBegin

-- W-07: Add a UNIQUE NULLS NOT DISTINCT constraint on the logical identity columns of a
-- statement so that concurrent extraction workers can use ON CONFLICT DO NOTHING
-- instead of crashing on duplicate inserts.
--
-- A generated hash column was rejected because val_date is TIMESTAMPTZ and
-- timestamptz::text is STABLE (TimeZone GUC-dependent), not IMMUTABLE.
-- NULLS NOT DISTINCT (PG 15+) treats NULLs as equal for uniqueness purposes, which
-- is correct here: the CHECK constraint guarantees exactly one val_* column is
-- non-NULL, so the tuple (subject, property, val_text=NULL, val_number=NULL,
-- val_date=<X>, val_entity=NULL) correctly matches another identical row.
--
-- The constraint is named uq_statements_content_hash to match the ON CONFLICT
-- clauses already present in application code.
ALTER TABLE statements
    ADD CONSTRAINT uq_statements_content_hash
    UNIQUE NULLS NOT DISTINCT (tenant_id, subject_id, property_id, val_text, val_number, val_date, val_entity);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE statements DROP CONSTRAINT IF EXISTS uq_statements_content_hash;
-- +goose StatementEnd
