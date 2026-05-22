"""Create relations table.

Revision ID: 0004
Revises: 0003
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB
from sqlalchemy import TIMESTAMP

revision = "0004"
down_revision = "0003"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "relations",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("source_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("target_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("type", sa.Text(), nullable=False),
        sa.Column("weight", sa.Numeric(), nullable=True),
        sa.Column("confidence", sa.Numeric(4, 3), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("meta", JSONB(), nullable=False, server_default="{}"),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=True,
        ),
        sa.Column("created_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        sa.UniqueConstraint(
            "tenant_id", "source_id", "target_id", "type",
            name="uq_relations_tenant_source_target_type",
        ),
    )
    op.execute(
        "ALTER TABLE relations ADD CONSTRAINT chk_relations_confidence "
        "CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))"
    )
    op.create_index("idx_relations_source", "relations", ["tenant_id", "source_id"])
    op.create_index("idx_relations_target", "relations", ["tenant_id", "target_id"])
    op.create_index("idx_relations_type", "relations", ["tenant_id", "type"])


def downgrade() -> None:
    op.drop_table("relations")
