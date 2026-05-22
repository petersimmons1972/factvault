"""Enable RLS on all domain tables and create tenant isolation policies

Revision ID: 0012
Revises: 0011
Create Date: 2026-05-22
"""
from alembic import op

revision = "0012"
down_revision = "0011"
branch_labels = None
depends_on = None

_DOMAIN_TABLES = [
    "entities",
    "properties",
    "statements",
    "qualifiers",
    "relations",
    "sources",
    "statement_sources",
    "source_verifications",
    "proposed_properties",
    "dossiers",
]


def upgrade() -> None:
    # Defensive: revoke any default PUBLIC privileges before granting to app_user.
    # Default Postgres grants no table privileges to PUBLIC, but downstream operators
    # may have customized this. Explicit revoke ensures app_user inherits ONLY what we
    # grant below, even on databases where PUBLIC has been granted unexpectedly.
    op.execute("REVOKE ALL ON ALL TABLES IN SCHEMA public FROM PUBLIC")
    op.execute("REVOKE ALL ON ALL SEQUENCES IN SCHEMA public FROM PUBLIC")

    # Policies use FOR ALL with USING only. Per Postgres semantics, when a policy
    # is declared FOR ALL and WITH CHECK is omitted, the USING expression is also
    # applied as WITH CHECK for INSERT and UPDATE. This means cross-tenant write
    # spoofing (e.g., INSERT INTO entities (..., tenant_id) VALUES (..., other_tenant))
    # is blocked: the new row's tenant_id is evaluated against current_setting(...)
    # and fails the policy, raising "new row violates row-level security policy".
    # Do NOT split this into separate FOR SELECT + FOR INSERT without explicitly
    # restoring WITH CHECK — silent loss of write-spoof protection would result.
    #
    # The NULLIF(current_setting('app.tenant_id', true), '')::uuid form handles
    # the "no GUC set" case: current_setting with missing_ok=true returns '' (empty
    # string), not NULL. NULLIF converts that to NULL, NULL::uuid is NULL, and
    # tenant_id = NULL evaluates to NULL (filtered out). Without NULLIF, unset GUC
    # would raise InvalidTextRepresentation when casting '' to uuid.

    for table in _DOMAIN_TABLES:
        op.execute(f"ALTER TABLE {table} ENABLE ROW LEVEL SECURITY")
        op.execute(f"ALTER TABLE {table} FORCE ROW LEVEL SECURITY")

    # Tables with a direct tenant_id column get the standard policy.
    # qualifiers and statement_sources join through their parent; qualifiers
    # gets a separate EXISTS-based policy. statement_sources has tenant_id directly
    # (it duplicates it for join-free RLS enforcement).
    _TENANT_ID_TABLES = [
        "entities",
        "properties",
        "statements",
        "relations",
        "sources",
        "statement_sources",
        "source_verifications",
        "proposed_properties",
        "dossiers",
    ]

    for table in _TENANT_ID_TABLES:
        op.execute(f"""
            CREATE POLICY tenant_isolation ON {table}
                USING (tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid)
        """)

    # qualifiers has no tenant_id; policy allows access when the parent statement
    # is accessible under the current tenant context.
    # NULLIF handles the case where app.tenant_id is unset (returns empty string
    # rather than NULL from current_setting(..., true)); casting '' to uuid
    # raises an error, so NULLIF('', '') → NULL → safe comparison.
    op.execute("""
        CREATE POLICY tenant_isolation ON qualifiers
            USING (
                EXISTS (
                    SELECT 1 FROM statements s
                    WHERE s.id = qualifiers.statement_id
                      AND s.tenant_id = NULLIF(current_setting('app.tenant_id', true), '')::uuid
                )
            )
    """)


def downgrade() -> None:
    for table in _DOMAIN_TABLES:
        op.execute(f"DROP POLICY IF EXISTS tenant_isolation ON {table}")
        op.execute(f"ALTER TABLE {table} NO FORCE ROW LEVEL SECURITY")
        op.execute(f"ALTER TABLE {table} DISABLE ROW LEVEL SECURITY")
