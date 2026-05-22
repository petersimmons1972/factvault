"""Create statement_sources junction table.

Revision ID: 0006
Revises: 0005
Create Date: 2026-05-22

Design note: id UUID PK (from spec DDL §3.1). A unique constraint on
(statement_id, source_id) enforces the one-excerpt-per-source-per-statement
invariant without a composite PK. The excerpt-offset check runs in
workers/extract.py before INSERT — this table does not enforce it at the
DB level; enforcement is application-layer.
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy import TIMESTAMP

revision = "0006"
down_revision = "0005"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "statement_sources",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "source_id",
            UUID(),
            sa.ForeignKey("sources.id"),
            nullable=False,
        ),
        sa.Column("excerpt", sa.Text(), nullable=False),
        sa.Column("excerpt_offset_start", sa.Integer(), nullable=False),
        sa.Column("excerpt_offset_end", sa.Integer(), nullable=False),
        sa.Column("extraction_method", sa.Text(), nullable=False),
        sa.Column("extracted_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        sa.Column("confidence", sa.Numeric(4, 3), nullable=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.UniqueConstraint(
            "statement_id", "source_id",
            name="uq_statement_sources_stmt_src",
        ),
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_offset_start "
        "CHECK (excerpt_offset_start >= 0)"
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_offset_end "
        "CHECK (excerpt_offset_end > excerpt_offset_start)"
    )
    op.execute(
        "ALTER TABLE statement_sources ADD CONSTRAINT chk_ss_confidence "
        "CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1))"
    )
    op.create_index("idx_stmt_sources_statement", "statement_sources", ["statement_id"])
    op.create_index("idx_stmt_sources_source", "statement_sources", ["source_id"])


def downgrade() -> None:
    op.drop_table("statement_sources")
