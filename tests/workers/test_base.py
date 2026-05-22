# tests/workers/test_base.py
import pytest
from factvault.workers.base import Worker, register_worker, get_worker, list_workers, _REGISTRY


@pytest.fixture(autouse=True)
def clean_registry():
    """Isolate registry state between tests."""
    original = _REGISTRY.copy()
    _REGISTRY.clear()
    yield
    _REGISTRY.clear()
    _REGISTRY.update(original)


def test_abc_enforces_abstract_method():
    """Instantiating Worker without implementing run() raises TypeError."""
    with pytest.raises(TypeError):
        Worker()  # type: ignore[abstract]


def test_registry_roundtrip():
    """register_worker + get_worker returns the same class."""
    @register_worker
    class DummyWorker(Worker):
        name = "dummy_test"
        def run(self, args) -> int:
            return 0

    assert get_worker("dummy_test") is DummyWorker


def test_list_workers_includes_registered():
    """list_workers() returns names of all registered workers."""
    @register_worker
    class DummyWorker(Worker):
        name = "dummy_test"
        def run(self, args) -> int:
            return 0

    names = list_workers()
    assert "dummy_test" in names


def test_run_returns_int():
    """Concrete Worker.run() must return an integer exit code."""
    @register_worker
    class ExitZeroWorker(Worker):
        name = "exit_zero_test"
        def run(self, args) -> int:
            return 0

    w = ExitZeroWorker()
    assert w.run({}) == 0


def test_register_worker_requires_name():
    """Registering a Worker subclass without a name raises ValueError."""
    with pytest.raises((ValueError, AttributeError)):
        @register_worker
        class NoNameWorker(Worker):
            def run(self, args) -> int:
                return 0


def test_double_register_raises():
    """Registering the same worker name twice raises ValueError."""
    @register_worker
    class FirstWorker(Worker):
        name = "duplicate_name"
        def run(self, args) -> int:
            return 0

    with pytest.raises(ValueError, match="already registered"):
        @register_worker
        class SecondWorker(Worker):
            name = "duplicate_name"
            def run(self, args) -> int:
                return 0


def test_get_worker_unknown_raises():
    """get_worker with an unknown name raises KeyError."""
    with pytest.raises(KeyError):
        get_worker("nonexistent_worker")
