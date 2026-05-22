"""Add embedding vector(1024) columns to entities, statements, relations, sources

Revision ID: 0010
Revises: 0009
Create Date: 2026-05-22
"""
from alembic import op

revision = "0010"
down_revision = "0009"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # All four embedding columns are nullable; populated by the embedding worker.
    for table in ("entities", "statements", "relations", "sources"):
        op.execute(
            f"ALTER TABLE {table} ADD COLUMN IF NOT EXISTS embedding vector(1024)"
        )


def downgrade() -> None:
    for table in ("entities", "statements", "relations", "sources"):
        op.execute(
            f"ALTER TABLE {table} DROP COLUMN IF EXISTS embedding"
        )
