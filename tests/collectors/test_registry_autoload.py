"""Verify all concrete collectors are registered on package import."""


def test_all_concrete_collectors_registered():
    """Verify the registry is populated by package-level imports alone."""
    import factvault.collectors  # triggers __init__.py
    from factvault.collectors.base import CollectorRegistry

    registry = CollectorRegistry.all()
    expected = {"http", "rss", "sitemap", "searxng", "wayback_cdx"}

    registered = set(registry.keys())
    missing = expected - registered
    assert not missing, f"Missing collectors after package import: {missing}. Registered: {registered}"


def test_get_each_registered_collector_works():
    """Verify each registered collector can be retrieved and has required attributes."""
    import factvault.collectors  # noqa: F401
    from factvault.collectors.base import get_collector

    for name in ("http", "rss", "sitemap", "searxng", "wayback_cdx"):
        cls = get_collector(name)
        assert cls is not None, f"get_collector({name!r}) returned None"
        # Verify it has the expected attribute
        assert hasattr(cls, "name"), f"{name}: missing 'name' class attr"
        assert cls.name == name, f"{name}: name attr mismatch (got {cls.name!r})"
