-- name: ListSourcesByTenant :many
SELECT id, tenant_id, url, fetched_at, content_hash, raw_html, raw_text, archive_url, publisher, title, published_at, embedding, last_verified_at, status, created_at, meta
FROM sources
WHERE tenant_id = $1
ORDER BY fetched_at DESC;
