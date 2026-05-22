"""proposed_properties strict-mode queue

Revision ID: 0008
Revises: 0007
Create Date: 2026-05-22
"""
from alembic import op

revision = "0008"
down_revision = "0007"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE proposed_properties (
            id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            tenant_id           UUID NOT NULL,
            proposed_slug       TEXT NOT NULL,
            proposed_value_type TEXT NOT NULL
                                CHECK (proposed_value_type IN
                                    ('entity_ref', 'string', 'number', 'date', 'url')),
            proposed_by         TEXT NOT NULL,
            example_excerpt     TEXT,
            example_source_id   UUID REFERENCES sources(id),
            status              TEXT NOT NULL DEFAULT 'pending'
                                CHECK (status IN ('pending', 'approved', 'rejected')),
            reviewed_by         TEXT,
            reviewed_at         TIMESTAMPTZ,
            created_at          TIMESTAMPTZ DEFAULT now(),
            UNIQUE (tenant_id, proposed_slug, status)
        )
    """)

    op.execute("""
        CREATE INDEX idx_proposed_properties_tenant_status
            ON proposed_properties (tenant_id, status)
    """)


def downgrade() -> None:
    op.execute("DROP TABLE IF EXISTS proposed_properties")
