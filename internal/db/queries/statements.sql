-- name: ListStatementsBySubject :many
SELECT id, tenant_id, subject_id, property_id, val_entity, val_text, val_number, val_date, val_json, rank, confidence, embedding, created_at
FROM statements
WHERE subject_id = $1
ORDER BY created_at DESC;
