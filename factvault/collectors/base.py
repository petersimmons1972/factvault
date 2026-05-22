"""
Collector ABC, RawDocument dataclass, and collector registry.

PLAN-BUG NOTES:
  - No SQLAlchemy date types here; stdlib datetime used throughout.
  - metadata field uses field(default_factory=dict) to avoid shared-mutable default.
"""
from __future__ import annotations

from abc import ABC, abstractmethod
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Iterator


@dataclass(frozen=True)
class RawDocument:
    """
    The raw output of a collector.

    - raw_html: raw bytes of the HTTP response body (NOT yet compressed).
    - raw_text: None at collect time; populated by the archive worker.
    - collector_name: set to Collector.name by the collector that produced this doc.
    - metadata: arbitrary collector-specific key/value pairs.
    """

    url: str
    raw_html: bytes
    fetched_at: datetime
    title: str | None = None
    raw_text: str | None = None
    published_at: datetime | None = None
    publisher: str | None = None
    collector_name: str | None = None
    metadata: dict[str, Any] = field(default_factory=dict)

    def __hash__(self) -> int:
        return hash((self.url, self.fetched_at))


class Collector(ABC):
    """Base class for all collectors."""

    name: str = ""

    @abstractmethod
    def fetch(self) -> Iterator[RawDocument]:
        """Yield collected documents."""


_REGISTRY: dict[str, type[Collector]] = {}


def register_collector(cls: type[Collector]) -> type[Collector]:
    """Register a Collector subclass by its ``name``."""
    if not cls.name:
        raise ValueError(f"Collector {cls!r} has no .name attribute set.")
    if cls.name in _REGISTRY:
        raise ValueError(
            f"Collector name '{cls.name}' is already registered by {_REGISTRY[cls.name]!r}."
        )
    _REGISTRY[cls.name] = cls
    return cls


def get_collector(name: str) -> type[Collector]:
    """Retrieve a registered Collector class by name."""
    if name not in _REGISTRY:
        raise KeyError(f"No collector registered with name '{name}'.")
    return _REGISTRY[name]


class CollectorRegistry:
    """Namespace alias for registry introspection."""

    @staticmethod
    def all() -> dict[str, type[Collector]]:
        return dict(_REGISTRY)
