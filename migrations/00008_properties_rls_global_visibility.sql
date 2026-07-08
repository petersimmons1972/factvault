-- +goose Up
-- +goose StatementBegin

-- Issue #269: the schema models properties.tenant_id IS NULL as global
-- properties (shared vocabulary), but the generic tenant_isolation RLS
-- policy applied to `properties` in 00001_initial_schema.sql only allows
-- `tenant_id = current_setting('app.tenant_id')`, which is false for NULL
-- rows. That hides global properties from app_user entirely, breaking
-- shared vocabulary/property reuse (internal/db/queries/properties.sql and
-- internal/workers/extract.go both expect `tenant_id = $1 OR tenant_id IS
-- NULL` to actually surface global rows under RLS).
--
-- Replace the generic policy with table-specific policies for `properties`:
--   SELECT: same tenant OR global (tenant_id IS NULL) rows are visible.
--   INSERT/UPDATE/DELETE: restricted to the caller's own tenant rows only,
--     so app_user can never create, mutate, or delete global rows. Global
--     rows remain immutable to app_user and are managed by admin/migration
--     roles that run outside RLS (superuser / BYPASSRLS).
DROP POLICY IF EXISTS tenant_isolation ON properties;

CREATE POLICY properties_select_tenant_or_global ON properties
FOR SELECT
USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR tenant_id IS NULL
);

CREATE POLICY properties_insert_tenant_only ON properties
FOR INSERT
WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
);

CREATE POLICY properties_update_tenant_only ON properties
FOR UPDATE
USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
)
WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
);

CREATE POLICY properties_delete_tenant_only ON properties
FOR DELETE
USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP POLICY IF EXISTS properties_delete_tenant_only ON properties;
DROP POLICY IF EXISTS properties_update_tenant_only ON properties;
DROP POLICY IF EXISTS properties_insert_tenant_only ON properties;
DROP POLICY IF EXISTS properties_select_tenant_or_global ON properties;

CREATE POLICY tenant_isolation ON properties
USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);

-- +goose StatementEnd
