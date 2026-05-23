-- +goose NO TRANSACTION

-- +goose Up
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_entities_embedding
    ON entities USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_statements_embedding
    ON statements USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_relations_embedding
    ON relations USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_sources_embedding
    ON sources USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS idx_entities_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_statements_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_relations_embedding;
DROP INDEX CONCURRENTLY IF EXISTS idx_sources_embedding;
