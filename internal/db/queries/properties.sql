-- name: ListPropertiesByTenant :many
SELECT id, tenant_id, slug, label, value_type, description
FROM properties
WHERE tenant_id = $1 OR tenant_id IS NULL
ORDER BY slug;
