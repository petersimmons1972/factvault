-- name: ListQualifiersByStatement :many
SELECT id, statement_id, property_id, val_text, val_number, val_date, val_entity
FROM qualifiers
WHERE statement_id = $1;
