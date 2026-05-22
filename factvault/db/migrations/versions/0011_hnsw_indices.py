"""HNSW indices on embedding columns (vector_cosine_ops, m=16, ef_construction=64)

Revision ID: 0011
Revises: 0010
Create Date: 2026-05-22
"""
from alembic import op

revision = "0011"
down_revision = "0010"
branch_labels = None
depends_on = None

_INDICES = [
    ("entities",   "idx_entities_embedding"),
    ("statements", "idx_statements_embedding"),
    ("relations",  "idx_relations_embedding"),
    ("sources",    "idx_sources_embedding"),
]


def upgrade() -> None:
    for table, idx_name in _INDICES:
        op.execute(
            f"CREATE INDEX IF NOT EXISTS {idx_name} "
            f"ON {table} USING hnsw (embedding vector_cosine_ops) "
            f"WITH (m = 16, ef_construction = 64)"
        )


def downgrade() -> None:
    for _, idx_name in _INDICES:
        op.execute(f"DROP INDEX IF EXISTS {idx_name}")
