import uuid
from datetime import datetime
from decimal import Decimal
from typing import Optional

from sqlalchemy import text, UniqueConstraint, CheckConstraint, ForeignKey, Index, Numeric, TIMESTAMP, LargeBinary
from sqlalchemy.dialects.postgresql import UUID, JSONB
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


class Base(DeclarativeBase):
    pass


class Entity(Base):
    __tablename__ = "entities"
    __table_args__ = (
        UniqueConstraint(
            "tenant_id", "ext_id",
            name="uq_entities_tenant_ext_id",
            postgresql_nulls_not_distinct=True,
        ),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    ext_id: Mapped[Optional[str]] = mapped_column(nullable=True)
    label: Mapped[str] = mapped_column(nullable=False)
    type_uri: Mapped[Optional[str]] = mapped_column(nullable=True)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)
    meta: Mapped[dict] = mapped_column(JSONB, nullable=False, server_default=text("'{}'"))
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )
    updated_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )


class Property(Base):
    __tablename__ = "properties"
    __table_args__ = (
        UniqueConstraint(
            "tenant_id", "slug",
            name="uq_properties_tenant_slug",
            postgresql_nulls_not_distinct=True,
        ),
        CheckConstraint(
            "value_type IN ('entity_ref', 'string', 'number', 'date', 'url')",
            name="chk_properties_value_type",
        ),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[Optional[uuid.UUID]] = mapped_column(UUID(as_uuid=True), nullable=True)
    slug: Mapped[str] = mapped_column(nullable=False)
    label: Mapped[str] = mapped_column(nullable=False)
    value_type: Mapped[str] = mapped_column(nullable=False)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)


class Statement(Base):
    __tablename__ = "statements"
    __table_args__ = (
        CheckConstraint(
            "rank IN ('preferred', 'normal', 'deprecated')",
            name="chk_statements_rank",
        ),
        CheckConstraint(
            "confidence >= 0 AND confidence <= 1",
            name="chk_statements_confidence",
        ),
        CheckConstraint(
            "(val_entity IS NOT NULL)::int + "
            "(val_text IS NOT NULL)::int + "
            "(val_number IS NOT NULL)::int + "
            "(val_date IS NOT NULL)::int = 1",
            name="chk_statement_value_populated",
        ),
        Index("idx_statements_subject", "subject_id", "property_id", "rank"),
        Index("idx_statements_tenant", "tenant_id", "subject_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    subject_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    property_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("properties.id"), nullable=False
    )
    val_entity: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=True
    )
    val_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    val_number: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    val_date: Mapped[Optional[datetime]] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    val_json: Mapped[Optional[dict]] = mapped_column(JSONB, nullable=True)
    rank: Mapped[str] = mapped_column(nullable=False, server_default=text("'normal'"))
    confidence: Mapped[Decimal] = mapped_column(Numeric(4, 3), nullable=False)
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )


class Qualifier(Base):
    __tablename__ = "qualifiers"
    __table_args__ = (
        CheckConstraint(
            "(val_entity IS NOT NULL)::int + "
            "(val_text IS NOT NULL)::int + "
            "(val_number IS NOT NULL)::int + "
            "(val_date IS NOT NULL)::int = 1",
            name="chk_qualifier_value_populated",
        ),
        Index("idx_qualifiers_statement", "statement_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    statement_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=False,
    )
    property_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("properties.id"), nullable=False
    )
    val_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    val_number: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    val_date: Mapped[Optional[datetime]] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    val_entity: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=True
    )


class Relation(Base):
    __tablename__ = "relations"
    __table_args__ = (
        UniqueConstraint(
            "tenant_id", "source_id", "target_id", "type",
            name="uq_relations_tenant_source_target_type",
        ),
        CheckConstraint(
            "confidence IS NULL OR (confidence >= 0 AND confidence <= 1)",
            name="chk_relations_confidence",
        ),
        Index("idx_relations_source", "tenant_id", "source_id"),
        Index("idx_relations_target", "tenant_id", "target_id"),
        Index("idx_relations_type", "tenant_id", "type"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    source_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    target_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("entities.id"), nullable=False
    )
    type: Mapped[str] = mapped_column(nullable=False)
    weight: Mapped[Optional[Decimal]] = mapped_column(Numeric(), nullable=True)
    confidence: Mapped[Optional[Decimal]] = mapped_column(Numeric(4, 3), nullable=True)
    description: Mapped[Optional[str]] = mapped_column(nullable=True)
    meta: Mapped[dict] = mapped_column(JSONB, nullable=False, server_default=text("'{}'"))
    statement_id: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=True,
    )
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )


class Source(Base):
    __tablename__ = "sources"
    __table_args__ = (
        UniqueConstraint("tenant_id", "url", name="uq_sources_tenant_url"),
        CheckConstraint(
            "status IN ('collected', 'archived', 'extracted', 'verified', 'link-rot', 'content-changed')",
            name="chk_sources_status",
        ),
        Index("idx_sources_tenant_status", "tenant_id", "status"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    url: Mapped[str] = mapped_column(nullable=False)
    fetched_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )
    content_hash: Mapped[str] = mapped_column(nullable=False)
    raw_html: Mapped[Optional[bytes]] = mapped_column(LargeBinary(), nullable=True)
    # raw_text is NULL until Stage 2 (archive worker) populates it permanently.
    # Downstream queries must only run after status = 'archived'.
    raw_text: Mapped[Optional[str]] = mapped_column(nullable=True)
    archive_url: Mapped[Optional[str]] = mapped_column(nullable=True)
    publisher: Mapped[Optional[str]] = mapped_column(nullable=True)
    title: Mapped[Optional[str]] = mapped_column(nullable=True)
    published_at: Mapped[Optional[datetime]] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    last_verified_at: Mapped[Optional[datetime]] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    status: Mapped[str] = mapped_column(nullable=False, server_default=text("'collected'"))
    created_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )


class ProposedProperty(Base):
    __tablename__ = "proposed_properties"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    proposed_slug: Mapped[str] = mapped_column(nullable=False)
    proposed_value_type: Mapped[str] = mapped_column(nullable=False)
    proposed_by: Mapped[str] = mapped_column(nullable=False)
    example_excerpt: Mapped[Optional[str]] = mapped_column(nullable=True)
    example_source_id: Mapped[Optional[uuid.UUID]] = mapped_column(
        UUID(as_uuid=True), ForeignKey("sources.id"), nullable=True
    )
    status: Mapped[str] = mapped_column(nullable=False, server_default=text("'pending'"))
    reviewed_by: Mapped[Optional[str]] = mapped_column(nullable=True)
    reviewed_at: Mapped[Optional[datetime]] = mapped_column(TIMESTAMP(timezone=True), nullable=True)
    created_at: Mapped[Optional[datetime]] = mapped_column(
        TIMESTAMP(timezone=True), server_default=text("now()")
    )


class SourceVerification(Base):
    __tablename__ = "source_verifications"

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    source_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("sources.id"), nullable=False
    )
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
    verified_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )
    status: Mapped[str] = mapped_column(nullable=False)
    new_content_hash: Mapped[Optional[str]] = mapped_column(nullable=True)
    notes: Mapped[Optional[str]] = mapped_column(nullable=True)


class StatementSource(Base):
    __tablename__ = "statement_sources"
    __table_args__ = (
        UniqueConstraint(
            "statement_id", "source_id",
            name="uq_statement_sources_stmt_src",
        ),
        CheckConstraint("excerpt_offset_start >= 0", name="chk_ss_offset_start"),
        CheckConstraint(
            "excerpt_offset_end > excerpt_offset_start", name="chk_ss_offset_end"
        ),
        CheckConstraint(
            "confidence IS NULL OR (confidence >= 0 AND confidence <= 1)",
            name="chk_ss_confidence",
        ),
        Index("idx_stmt_sources_statement", "statement_id"),
        Index("idx_stmt_sources_source", "source_id"),
    )

    id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), primary_key=True, server_default=text("gen_random_uuid()")
    )
    statement_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True),
        ForeignKey("statements.id", ondelete="CASCADE"),
        nullable=False,
    )
    source_id: Mapped[uuid.UUID] = mapped_column(
        UUID(as_uuid=True), ForeignKey("sources.id"), nullable=False
    )
    excerpt: Mapped[str] = mapped_column(nullable=False)
    excerpt_offset_start: Mapped[int] = mapped_column(nullable=False)
    excerpt_offset_end: Mapped[int] = mapped_column(nullable=False)
    extraction_method: Mapped[str] = mapped_column(nullable=False)
    extracted_at: Mapped[datetime] = mapped_column(
        TIMESTAMP(timezone=True), nullable=False, server_default=text("now()")
    )
    confidence: Mapped[Optional[Decimal]] = mapped_column(Numeric(4, 3), nullable=True)
    tenant_id: Mapped[uuid.UUID] = mapped_column(UUID(as_uuid=True), nullable=False)
