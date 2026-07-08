"""Tests for rag.search.filters value-shape dispatcher."""

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
