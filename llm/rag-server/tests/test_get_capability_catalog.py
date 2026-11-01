from contextlib import contextmanager
from types import SimpleNamespace

import pytest

from rag.core.utils import db_query


@pytest.fixture(autouse=True)
def _reset_cache():
    # The cache is a bare module-level global (see the migration/design
    # comment: single global entry, not account-keyed), so it must be reset
    # between tests or an earlier test's cached result leaks into this one.
    db_query._capability_catalog_cache = None
    yield
    db_query._capability_catalog_cache = None


def _row(name, surface="agent_tool", scope="pass_through", description="desc"):
    return SimpleNamespace(name=name, surface=surface, scope=scope, description=description)


def _fake_engine(rows=None, raise_error=False, call_counter=None):
    @contextmanager
    def connect():
        if call_counter is not None:
            call_counter.append(1)
        if raise_error:
            raise RuntimeError("db unavailable")

        class _Conn:
            def execute(self, query):
                return rows or []

        yield _Conn()

    return SimpleNamespace(connect=connect)


def test_returns_parsed_rows(monkeypatch):
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[_row("kubectl_execute")]))

    result = db_query.get_capability_catalog()

    assert result == [
        {"name": "kubectl_execute", "surface": "agent_tool", "scope": "pass_through", "description": "desc"}
    ]


def test_includes_unclassified_entries_rather_than_filtering_them_out(monkeypatch):
    # An unclassified row must reach the caller so it can be treated as a
    # gap, never silently dropped as if it didn't exist.
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[_row("brand_new_task", scope="unclassified")]))

    result = db_query.get_capability_catalog()

    assert result[0]["scope"] == "unclassified"


def test_db_failure_returns_empty_list_not_stale_or_partial_data(monkeypatch):
    monkeypatch.setattr(db_query, "engine", _fake_engine(raise_error=True))

    assert db_query.get_capability_catalog() == []


def test_cached_result_does_not_hit_the_db_twice_within_ttl(monkeypatch):
    calls = []
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[_row("kubectl_execute")], call_counter=calls))

    first = db_query.get_capability_catalog()
    second = db_query.get_capability_catalog()

    assert first == second
    assert len(calls) == 1


def test_expired_cache_refetches(monkeypatch):
    calls = []
    monkeypatch.setattr(db_query, "engine", _fake_engine(rows=[_row("kubectl_execute")], call_counter=calls))
    monkeypatch.setattr(db_query, "_CAPABILITY_CATALOG_CACHE_TTL_SECONDS", 0)

    db_query.get_capability_catalog()
    db_query.get_capability_catalog()

    assert len(calls) == 2
