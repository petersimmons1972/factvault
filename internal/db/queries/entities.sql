-- name: GetEntity :one
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE id = $1;

-- name: ListEntitiesByTenant :many
SELECT id, tenant_id, ext_id, label, type_uri, description, embedding, meta, created_at, updated_at
FROM entities
WHERE tenant_id = $1
ORDER BY created_at DESC;
