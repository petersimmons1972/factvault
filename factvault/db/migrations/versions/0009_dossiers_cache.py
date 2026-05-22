"""dossiers cache table

Revision ID: 0009
Revises: 0008
Create Date: 2026-05-22
"""
from alembic import op

revision = "0009"
down_revision = "0008"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE dossiers (
            id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id    UUID NOT NULL,
            entity_id    UUID NOT NULL REFERENCES entities(id),
            assembled_at TIMESTAMPTZ NOT NULL DEFAULT now(),
            bundle       JSONB NOT NULL,
            UNIQUE (tenant_id, entity_id)
        )
    """)

    op.execute("""
        CREATE INDEX idx_dossiers_tenant_assembled
            ON dossiers (tenant_id, assembled_at DESC)
    """)


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS dossiers")
