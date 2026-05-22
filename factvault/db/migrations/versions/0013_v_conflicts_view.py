"""v_conflicts view — surfaces non-deprecated statement conflicts

Revision ID: 0013
Revises: 0012
Create Date: 2026-05-22
"""
from alembic import op

revision = "0013"
down_revision = "0012"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE OR REPLACE VIEW v_conflicts AS
        SELECT
            subject_id,
            property_id,
            tenant_id,
            COUNT(*) AS competing_count,
            array_agg(id) AS statement_ids
        FROM statements
        WHERE rank != 'deprecated'
        GROUP BY subject_id, property_id, tenant_id
        HAVING COUNT(
            DISTINCT COALESCE(
                val_entity::text,
                val_text,
                val_number::text,
                val_date::text,
                val_json::text
            )
        ) > 1
    """)


def downgrade() -> None:
    op.execute("DROP VIEW IF EXISTS v_conflicts")
