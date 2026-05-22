"""Create sources table.

Revision ID: 0005
Revises: 0004
Create Date: 2026-05-22

Invariant: raw_text is NULL until Stage 2 (archive worker) populates it.
Any query reading raw_text must only run after status >= 'archived'.
Downstream processing reads raw_text; it never re-fetches the URL.
"""
from alembic import op
import sqlalchemy as sa
from sqlalchemy.dialects.postgresql import UUID
from sqlalchemy import TIMESTAMP

revision = "0005"
down_revision = "0004"
branch_labels = None
depends_on = None

_STATUS_VALUES = "('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')"


def upgrade() -> None:
    op.create_table(
        "sources",
        sa.Column("id", UUID(), server_default=sa.text("gen_random_uuid()"), primary_key=True),
        sa.Column("tenant_id", UUID(), nullable=False),
        sa.Column("url", sa.Text(), nullable=False),
        sa.Column("fetched_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        sa.Column("content_hash", sa.Text(), nullable=False),
        sa.Column("raw_html", sa.LargeBinary(), nullable=True),
        # raw_text is NULL until Stage 2 (archive worker) populates it permanently.
        # No downstream query should read raw_text before status = 'archived'.
        sa.Column("raw_text", sa.Text(), nullable=True),
        sa.Column("archive_url", sa.Text(), nullable=True),
        sa.Column("publisher", sa.Text(), nullable=True),
        sa.Column("title", sa.Text(), nullable=True),
        sa.Column("published_at", TIMESTAMP(timezone=True), nullable=True),
        sa.Column("last_verified_at", TIMESTAMP(timezone=True), nullable=True),
        sa.Column(
            "status",
            sa.Text(),
            nullable=False,
            server_default="collected",
        ),
        sa.Column("created_at", TIMESTAMP(timezone=True), nullable=False, server_default=sa.text("now()")),
        sa.UniqueConstraint("tenant_id", "url", name="uq_sources_tenant_url"),
    )
    op.execute(
        f"ALTER TABLE sources ADD CONSTRAINT chk_sources_status "
        f"CHECK (status IN {_STATUS_VALUES})"
    )
    op.create_index("idx_sources_tenant_status", "sources", ["tenant_id", "status"])
    op.create_index("idx_sources_last_verified", "sources", ["last_verified_at"])
    op.create_index("idx_sources_published_at", "sources", ["published_at"])


def downgrade() -> None:
    op.drop_table("sources")
