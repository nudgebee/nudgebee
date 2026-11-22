"""Tests for rag.search.filters value-shape dispatcher."""

import os

from qdrant_client.models import MatchAny, MatchValue, Range

from rag.search.filters import build_field_condition, build_metadata_filter


def test_scalar_value_builds_match_value():
    cond = build_field_condition("agent_module", "k8s_ops")
    assert cond.key == "metadata.agent_module"
    assert isinstance(cond.match, MatchValue)
    assert cond.match.value == "k8s_ops"


def test_list_value_builds_match_any():
    cond = build_field_condition("kind", ["frequent_service", "diagnostic_style"])
    assert cond.key == "metadata.kind"
    assert isinstance(cond.match, MatchAny)
    assert list(cond.match.any) == ["frequent_service", "diagnostic_style"]


def test_dict_with_gte_builds_range():
    cond = build_field_condition("expires_at", {"gte": 1234567890})
    assert cond.key == "metadata.expires_at"
    assert cond.match is None
    assert isinstance(cond.range, Range)
    assert cond.range.gte == 1234567890
    assert cond.range.gt is None
    assert cond.range.lte is None
    assert cond.range.lt is None


def test_dict_with_full_range_builds_range():
    cond = build_field_condition("timestamp", {"gt": 100, "lt": 200})
    assert isinstance(cond.range, Range)
    assert cond.range.gt == 100
    assert cond.range.lt == 200


def test_metadata_prefix_added_when_absent():
    cond = build_field_condition("kind", "x")
    assert cond.key == "metadata.kind"


def test_metadata_prefix_preserved_when_present():
    cond = build_field_condition("metadata.kind", "x")
    assert cond.key == "metadata.kind"


def test_build_metadata_filter_mixes_shapes():
    # Real memory-v2 shape: agent_module equality + expires_at range +
    # kind IN, all in a single filter call.
    f = build_metadata_filter(
        custom_fields={
            "agent_module": "k8s_ops",
            "expires_at": {"gte": 1_700_000_000},
            "kind": ["frequent_service", "diagnostic_style"],
        }
    )
    assert f is not None
    conds = f.must
    assert conds is not None
    assert len(conds) == 3

    by_key = {c.key: c for c in conds}
    assert isinstance(by_key["metadata.agent_module"].match, MatchValue)
    assert isinstance(by_key["metadata.kind"].match, MatchAny)
    assert isinstance(by_key["metadata.expires_at"].range, Range)


def test_build_metadata_filter_scalar_only_regression():
    # Pre-refactor callers pass only scalars — behaviour must not change.
    f = build_metadata_filter(custom_fields={"kind": "frequent_service"})
    assert f is not None
    conds = f.must
    assert conds is not None
    assert len(conds) == 1
    assert isinstance(conds[0].match, MatchValue)
    assert conds[0].match.value == "frequent_service"


def test_empty_filter_returns_none():
    assert build_metadata_filter() is None
    assert build_metadata_filter(custom_fields={}) is None


def test_must_not_only_filter_is_structured():
    from rag.search.filters import build_filter_from_dict, is_structured_filter

    spec = {"must_not": [{"field": "source", "value": "nudgebee_docs"}]}
    assert is_structured_filter(spec)
    built = build_filter_from_dict(spec)
    assert built.must is None
    assert len(built.must_not) == 1
    assert built.must_not[0].key == "metadata.source"
    assert built.must_not[0].match.value == "nudgebee_docs"


def test_flat_filter_is_not_structured():
    from rag.search.filters import is_structured_filter

    assert not is_structured_filter({"source": "nudgebee_docs"})
    assert not is_structured_filter({})
    assert not is_structured_filter(None)


def _capture_search_filter(monkeypatch, metadata_filter):
    # search_logic imports utils.config, which refuses to load without these.
    for name, value in {
        "APP_DATABASE_URL": "postgres://test",
        "RELAY_SERVER_ENDPOINT": "http://test",
        "RELAY_SERVER_SECRET_KEY": "test",
    }.items():
        monkeypatch.setenv(name, os.environ.get(name, value))
    from rag.search.search_logic import _search_single_collection_by_vector

    captured = {}

    class _Response:
        points = []

    class _Client:
        def query_points(self, **kwargs):
            captured.update(kwargs)
            return _Response()

    _search_single_collection_by_vector("kb_test", [0.0], _Client(), 3, metadata_filter)
    return captured["query_filter"]


def test_search_applies_must_not_filter(monkeypatch):
    query_filter = _capture_search_filter(monkeypatch, {"must_not": [{"field": "source", "value": "nudgebee_docs"}]})
    assert query_filter.must is None
    assert query_filter.must_not[0].key == "metadata.source"


def test_search_keeps_flat_filter_semantics(monkeypatch):
    query_filter = _capture_search_filter(monkeypatch, {"agent_module": "k8s_ops", "kind": ["a", "b"]})
    assert [c.key for c in query_filter.must] == ["metadata.agent_module", "metadata.kind"]
    assert query_filter.must_not is None


def test_structured_filter_keeps_flat_keys_as_must():
    from rag.search.filters import build_filter_from_dict

    built = build_filter_from_dict(
        {"agent_module": "k8s_ops", "must_not": [{"field": "source", "value": "nudgebee_docs"}]}
    )
    assert [c.key for c in built.must] == ["metadata.agent_module"]
    assert built.must_not[0].key == "metadata.source"


def test_search_rejects_structured_filter_without_valid_conditions(monkeypatch):
    for name, value in {
        "APP_DATABASE_URL": "postgres://test",
        "RELAY_SERVER_ENDPOINT": "http://test",
        "RELAY_SERVER_SECRET_KEY": "test",
    }.items():
        monkeypatch.setenv(name, os.environ.get(name, value))
    from rag.search.search_logic import _search_single_collection_by_vector

    called = []

    class _Client:
        def query_points(self, **kwargs):
            called.append(kwargs)

    # "any" is a typo for "values": the clause is dropped, so nothing would be excluded.
    docs = _search_single_collection_by_vector(
        "kb_test", [0.0], _Client(), 3, {"must_not": [{"field": "source", "any": ["nudgebee_docs"]}]}
    )
    assert docs == []
    assert called == [], "a filter that excludes nothing must not run unfiltered"


def test_search_rejects_partly_malformed_structured_filter(monkeypatch):
    for name, value in {
        "APP_DATABASE_URL": "postgres://test",
        "RELAY_SERVER_ENDPOINT": "http://test",
        "RELAY_SERVER_SECRET_KEY": "test",
    }.items():
        monkeypatch.setenv(name, os.environ.get(name, value))
    from rag.search.search_logic import _search_single_collection_by_vector

    called = []

    class _Client:
        def query_points(self, **kwargs):
            called.append(kwargs)

    # A valid "must" must not let a dropped (typo'd) exclusion through.
    docs = _search_single_collection_by_vector(
        "kb_test",
        [0.0],
        _Client(),
        3,
        {
            "must": [{"field": "module", "value": "knowledge_base"}],
            "must_not": [{"field": "source", "any": ["nudgebee_docs"]}],
        },
    )
    assert docs == []
    assert called == []


def test_structured_filter_rejects_malformed_conditions():
    import pytest

    from rag.search.filters import build_filter_from_dict

    for spec in (
        {"must_not": [{"field": "source", "any": ["nudgebee_docs"]}]},  # no value/values/range
        {"must_not": [{"value": "nudgebee_docs"}]},  # no field
        {"must_not": "nudgebee_docs"},  # clause is not a list
        {"must_not": ["source"]},  # condition is not a dict
    ):
        with pytest.raises(ValueError):
            build_filter_from_dict(spec)
