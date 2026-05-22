"""source_verifications append-only log

Revision ID: 0007
Revises: 0006
Create Date: 2026-05-22
"""
from alembic import op

revision = "0007"
down_revision = "0006"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.execute("""
        CREATE TABLE source_verifications (
            id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
            source_id        UUID NOT NULL REFERENCES sources(id),
            tenant_id        UUID NOT NULL,
            verified_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
            status           TEXT NOT NULL
                             CHECK (status IN ('live', 'link-rot', 'content-changed', 'excerpt-missing')),
            new_content_hash TEXT,
            notes            TEXT
        )
    """)

    op.execute("""
        CREATE INDEX idx_source_verifications_source
            ON source_verifications (source_id, verified_at DESC)
    """)

    op.execute("""
        CREATE INDEX idx_source_verifications_status
            ON source_verifications (status, verified_at DESC)
    """)

    # Append-only enforcement trigger
    op.execute("""
        CREATE OR REPLACE FUNCTION deny_source_verifications_mutation()
        RETURNS TRIGGER LANGUAGE plpgsql AS $$
        BEGIN
            RAISE EXCEPTION 'source_verifications is append-only. DELETE and UPDATE are forbidden.';
        END;
        $$
    """)

    op.execute("""
        CREATE TRIGGER trg_source_verifications_no_update
            BEFORE UPDATE ON source_verifications
            FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation()
    """)

    op.execute("""
        CREATE TRIGGER trg_source_verifications_no_delete
            BEFORE DELETE ON source_verifications
            FOR EACH ROW EXECUTE FUNCTION deny_source_verifications_mutation()
    """)


def downgrade() -> None:
    op.execute("DROP TRIGGER IF EXISTS trg_source_verifications_no_delete ON source_verifications")
    op.execute("DROP TRIGGER IF EXISTS trg_source_verifications_no_update ON source_verifications")
    op.execute("DROP FUNCTION IF EXISTS deny_source_verifications_mutation()")
    op.execute("DROP TABLE IF EXISTS source_verifications")
