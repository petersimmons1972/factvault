"""Create entities and properties tables.

Revision ID: 0002
Revises: 0001
Create Date: 2026-05-22
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID, JSONB
from sqlalchemy import TIMESTAMP

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "entities",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("ext_id", sa.Text(), nullable=True),
        sa.Column("label", sa.Text(), nullable=False),
        sa.Column("type_uri", sa.Text(), nullable=True),
        sa.Column("description", sa.Text(), nullable=True),
        sa.Column("meta", JSONB(), nullable=False, server_default="{}"),
        sa.Column("created_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        sa.Column("updated_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        # UNIQUE (tenant_id, ext_id) only when ext_id is NOT NULL — use NULLS NOT DISTINCT
        sa.UniqueConstraint("tenant_id", "ext_id", name="uq_entities_tenant_ext_id",
                            postgresql_nulls_not_distinct=True),
    )
    op.create_index("idx_entities_tenant", "entities", ["tenant_id"])
    op.create_index(
        "idx_entities_label", "entities",
        ["tenant_id", sa.text("lower(label)")],
        postgresql_using="btree",
    )
    op.create_index("idx_entities_type", "entities", ["tenant_id", "type_uri"])

    op.create_table(
        "properties",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=True),  # NULL = system-wide
        sa.Column("slug", sa.Text(), nullable=False),
        sa.Column("label", sa.Text(), nullable=False),
        sa.Column(
            "value_type",
            sa.Text(),
            nullable=False,
            # CHECK inline via op.execute below — SQLAlchemy DDL CHECK on Text
        ),
        sa.Column("description", sa.Text(), nullable=True),
        sa.UniqueConstraint("tenant_id", "slug", name="uq_properties_tenant_slug",
                            postgresql_nulls_not_distinct=True),
    )
    op.execute(
        "ALTER TABLE properties ADD CONSTRAINT chk_properties_value_type "
        "CHECK (value_type IN ('entity_ref', 'string', 'number', 'date', 'url'))"
    )


def downgrade() -> None:
    op.drop_table("properties")
    op.drop_table("entities")
