-- +goose Up
-- +goose StatementBegin

-- Root cause (#267): the generic tenant_isolation policy applied to every table
-- in 00001_initial_schema.sql (`tenant_id = current_setting('app.tenant_id')`)
-- was also applied to `properties`. But `properties.tenant_id` is intentionally
-- nullable — NULL means a shared/global property visible to every tenant (see
-- the `uq_properties_tenant_slug` UNIQUE NULLS NOT DISTINCT constraint, and the
-- explicit `tenant_id = $2 OR tenant_id IS NULL` lookup already used in
-- internal/workers/extract.go). Because `properties` is FORCE ROW LEVEL
-- SECURITY, the generic policy silently hid every global (tenant_id IS NULL)
-- property row from all app_user-scoped queries, including the
-- `statements JOIN properties` in internal/assembler/bundle.go. Any statement
-- whose property was global (or any tenant-scoped query joining through such a
-- property) was dropped entirely from the assembled bundle, which is why
-- POST /briefs/generate produced key_claims=null even though the underlying
-- statement rows existed with confidence above the extraction threshold.
--
-- Fix: SELECT/USING allows both the caller's own tenant rows and global rows.
-- WITH CHECK is left restricted to the caller's own tenant so that app_user
-- (tenant-scoped requests) cannot itself create/mutate global properties;
-- global properties are only ever inserted with an explicit real tenant_id by
-- application code (extract.go, examples/loader.go), so this does not change
-- existing write behavior.
DROP POLICY IF EXISTS tenant_isolation ON properties;
CREATE POLICY tenant_isolation ON properties
USING (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
    OR tenant_id IS NULL
)
WITH CHECK (
    tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
);
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP POLICY IF EXISTS tenant_isolation ON properties;
CREATE POLICY tenant_isolation ON properties
USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid);
-- +goose StatementEnd
