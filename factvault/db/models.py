import uuid
from datetime import datetime
from decimal import Decimal
from typing import Optional

from sqlalchemy import text, UniqueConstraint, CheckConstraint, ForeignKey, Index, Numeric, TIMESTAMP
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
