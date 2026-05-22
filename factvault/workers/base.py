# factvault/workers/base.py
from __future__ import annotations

import abc
from typing import Any

_REGISTRY: dict[str, type["Worker"]] = {}


class Worker(abc.ABC):
    """Abstract base class for all pipeline workers."""

    name: str  # subclasses MUST set as class attribute

    @abc.abstractmethod
    def run(self, args: dict[str, Any]) -> int:
        """Execute the worker.

        Args:
          args: parsed CLI args as a dict (tenant_id, once, interval, etc.)

        Returns:
          Exit code (0 = success, non-zero = failure).
        """


def register_worker(cls: type[Worker]) -> type[Worker]:
    """Class decorator that registers a Worker subclass by name."""
    if not issubclass(cls, Worker):
        raise TypeError("register_worker requires a Worker subclass")
    if not getattr(cls, "name", None):
        raise ValueError("Worker subclass must set a `name` class attribute")
    if cls.name in _REGISTRY:
        raise ValueError(f"Worker {cls.name!r} already registered")
    _REGISTRY[cls.name] = cls
    return cls


def get_worker(name: str) -> type[Worker]:
    """Return the Worker class registered under *name*.

    Raises KeyError if no worker with that name exists.
    """
    if name not in _REGISTRY:
        raise KeyError(f"No worker registered with name '{name}'")
    return _REGISTRY[name]


def list_workers() -> list[str]:
    """Return sorted list of all registered worker names."""
    return sorted(_REGISTRY.keys())
