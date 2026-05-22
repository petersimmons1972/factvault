# tests/workers/test_cli.py
import pytest
from click.testing import CliRunner
from factvault.workers.cli import main
from factvault.workers.base import register_worker, Worker, _REGISTRY


@pytest.fixture(autouse=True)
def clean_registry():
    """Isolate registry state between tests."""
    original = _REGISTRY.copy()
    _REGISTRY.clear()
    yield
    _REGISTRY.clear()
    _REGISTRY.update(original)


@pytest.fixture
def runner():
    return CliRunner()


def test_list_command_returns_zero(runner):
    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0


def test_list_command_shows_registered_worker(runner):
    @register_worker
    class VisibleWorker(Worker):
        """A visible test worker."""
        name = "visible_for_cli_test"
        def run(self, args) -> int:
            return 0

    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0
    assert "visible_for_cli_test" in result.output


def test_run_unknown_worker_exits_nonzero(runner):
    result = runner.invoke(main, ["run", "nonexistent_worker_xyz"])
    assert result.exit_code != 0


def test_run_command_invokes_worker(runner):
    @register_worker
    class NopWorker(Worker):
        """No-op worker for testing."""
        name = "nop_cli_test"
        def run(self, args) -> int:
            return 0

    result = runner.invoke(main, ["run", "nop_cli_test", "--once"])
    assert result.exit_code == 0


def test_list_shows_docstring_description(runner):
    @register_worker
    class DocWorker(Worker):
        """First line of docstring.

        This second line should not appear.
        """
        name = "doc_test_worker"
        def run(self, args) -> int:
            return 0

    result = runner.invoke(main, ["list"])
    assert result.exit_code == 0
    assert "doc_test_worker" in result.output
    assert "First line of docstring." in result.output
