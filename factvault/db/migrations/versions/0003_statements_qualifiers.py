"""Create statements and qualifiers tables.

Revision ID: 0003
Revises: 0002
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB
from sqlalchemy import TIMESTAMP

revision = "0003"
down_revision = "0002"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "statements",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("subject_id", UUID(), sa.ForeignKey("entities.id"), nullable=False),
        sa.Column("property_id", UUID(), sa.ForeignKey("properties.id"), nullable=False),
        sa.Column("val_entity", UUID(), sa.ForeignKey("entities.id"), nullable=True),
        sa.Column("val_text", sa.Text(), nullable=True),
        sa.Column("val_number", sa.Numeric(), nullable=True),
        sa.Column("val_date", TIMESTAMP(timezone=True), nullable=True),
        sa.Column("val_json", JSONB(), nullable=True),
        sa.Column(
            "rank", sa.Text(), nullable=False, server_default="normal"
        ),
        sa.Column(
            "confidence",
            sa.Numeric(4, 3),
            nullable=False,
        ),
        sa.Column("created_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statements_rank "
        "CHECK (rank IN ('preferred', 'normal', 'deprecated'))"
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statements_confidence "
        "CHECK (confidence >= 0 AND confidence <= 1)"
    )
    op.execute(
        "ALTER TABLE statements ADD CONSTRAINT chk_statement_value_populated "
        "CHECK ("
        "    (val_entity IS NOT NULL)::int + "
        "    (val_text   IS NOT NULL)::int + "
        "    (val_number IS NOT NULL)::int + "
        "    (val_date   IS NOT NULL)::int = 1"
        ")"
    )
    op.create_index("idx_statements_subject", "statements", ["subject_id", "property_id", "rank"])
    op.create_index("idx_statements_tenant", "statements", ["tenant_id", "subject_id"])
    op.create_index(
        "idx_statements_val_entity", "statements", ["val_entity"],
        postgresql_where=sa.text("val_entity IS NOT NULL"),
    )
    op.create_index("idx_statements_confidence", "statements", [sa.text("confidence DESC")])

    op.create_table(
        "qualifiers",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column(
            "statement_id",
            UUID(),
            sa.ForeignKey("statements.id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column("property_id", UUID(), sa.ForeignKey("properties.id"), nullable=False),
        sa.Column("val_text", sa.Text(), nullable=True),
        sa.Column("val_number", sa.Numeric(), nullable=True),
        sa.Column("val_date", TIMESTAMP(timezone=True), nullable=True),
        sa.Column("val_entity", UUID(), sa.ForeignKey("entities.id"), nullable=True),
    )
    op.execute(
        "ALTER TABLE qualifiers ADD CONSTRAINT chk_qualifier_value_populated "
        "CHECK ("
        "    (val_entity IS NOT NULL)::int + "
        "    (val_text   IS NOT NULL)::int + "
        "    (val_number IS NOT NULL)::int + "
        "    (val_date   IS NOT NULL)::int = 1"
        ")"
    )
    op.create_index("idx_qualifiers_statement", "qualifiers", ["statement_id"])


def downgrade() -> None:
    op.drop_table("qualifiers")
    op.drop_table("statements")
