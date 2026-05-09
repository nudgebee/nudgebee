"""Signoz log agent benchmark — via LogDefaultAgent pipeline.

Agent: logs (routes to logs_default for Signoz provider)
Tools: logs_execute, query_generator, resource_search
Data: fixtures/ directory with test_case.yaml files

Answer extraction: Fetches the query_generator agent's response from
llm_conversation_agent table (the generated JSON filter), since the
query_generator stores its output directly in the agent response — not
in llm_conversation_tool_calls.

Evaluation: Structural JSON comparison (not RAGAS semantic similarity).
Compares expected vs actual JSON filters by checking field names,
operators, and values. Overwrites answer_similarity with the structural
score so it persists to DB.
"""

import json
import logging
import os
from pathlib import Path
from typing import Any, Dict, List, Optional, Set, Tuple

import pytest

from benchmark_server.utils.llm_client import extract_conversation_id
from llm.agents.common.metrics import _db_engine
from llm.agents.common.runner import run_benchmark

logger = logging.getLogger(__name__)

AGENT = "logs"
TOOL_NAMES = ["logs_execute", "query_generator", "resource_search"]
FIXTURES_DIR = Path(__file__).parent / "fixtures"


# ---------------------------------------------------------------------------
# Answer extraction
# ---------------------------------------------------------------------------


def _fetch_query_generator_response(
    conversation_id: str, account_id: str
) -> Optional[str]:
    """Fetch the query_generator agent's response from the DB.

    The query_generator agent stores its generated JSON filter directly
    in llm_conversation_agent.response (not in tool_calls).
    """
    if not conversation_id or not _db_engine:
        return None

    try:
        from sqlalchemy import text

        with _db_engine.connect() as conn:
            result = conn.execute(
                text(
                    """
                    SELECT response
                    FROM llm_conversation_agent
                    WHERE conversation_id = :conversation_id
                      AND account_id = :account_id
                      AND agent_name = 'query_generator'
                      AND status = 'success'
                      AND response IS NOT NULL
                    ORDER BY created_at DESC
                    LIMIT 1
                    """
                ),
                {
                    "conversation_id": conversation_id,
                    "account_id": account_id,
                },
            ).fetchone()

            if result and result[0]:
                return str(result[0]).strip()
    except Exception as e:
        logger.error(
            "Failed to fetch query_generator response for %s: %s",
            conversation_id,
            e,
        )
    return None


def _extract_signoz_query(
    response_data: dict,
    account_id: str,
    tool_names: List[str],
    db_tool_name: Optional[str] = None,
) -> List[str]:
    """Extract the generated SigNoz log query filter.

    Fetches the query_generator agent's JSON response from
    llm_conversation_agent. Falls back to the full response text.
    """
    if not response_data:
        return []

    conversation_id = extract_conversation_id(response_data)

    # Primary: fetch query_generator's JSON filter from agent table
    if conversation_id:
        query_filter = _fetch_query_generator_response(
            conversation_id, account_id
        )
        if query_filter:
            return [query_filter]

    # Fallback: return the full response text
    from benchmark_server.utils.llm_client import extract_response_text

    resp = extract_response_text(response_data)
    if resp:
        return [resp] if isinstance(resp, str) else resp

    return []


# ---------------------------------------------------------------------------
# Structural JSON evaluation
# ---------------------------------------------------------------------------

# Operators that are semantically equivalent for matching purposes
_LIKE_OPS = {"_ilike", "_like", "_nlike"}
_EQ_OPS = {"_eq", "_neq"}
_RANGE_OPS = {"_gt", "_gte", "_lt", "_lte"}
_LIST_OPS = {"_in", "_nin"}


_RESERVED_KEYS = {"where", "time_range", "limit", "start_time", "_or", "_and"}


def _extract_field_conditions(
    mapping: dict, conditions: Set[Tuple[str, str, str]]
):
    """Extract (field, operator, value) triples from a dict of field->ops.

    Handles nested ``_or``/``_and`` inside where clause and expands ``_in``
    lists into individual values for matching.
    """
    for field, ops in mapping.items():
        # _or / _and nested inside where
        if field in ("_or", "_and") and isinstance(ops, list):
            for item in ops:
                if isinstance(item, dict):
                    _extract_field_conditions(item, conditions)
            continue
        if isinstance(ops, dict):
            for op, val in ops.items():
                if op in ("_in", "_nin") and isinstance(val, list):
                    # Expand list: _in ["WARN","ERROR"] → individual values
                    for v in val:
                        conditions.add((field, op, str(v).lower()))
                else:
                    conditions.add((field, op, str(val).lower()))
        else:
            conditions.add((field, "_eq", str(ops).lower()))


def _extract_conditions(
    obj: dict, prefix: str = ""
) -> Set[Tuple[str, str, str]]:
    """Extract (field, operator, value) triples from a query JSON.

    Handles two formats the query_generator may produce:
      1. Wrapped:  ``{"where": {"field": {"_op": "val"}}, "time_range": "2h"}``
      2. Flat:     ``{"field": {"_op": "val"}}``  (no ``where`` key)

    Also handles ``_or``/``_and`` at top level or nested inside ``where``.
    """
    conditions: Set[Tuple[str, str, str]] = set()

    where = obj.get("where", {})
    if isinstance(where, dict) and where:
        _extract_field_conditions(where, conditions)
    elif "where" not in obj:
        # No "where" key — treat non-reserved top-level keys as field conditions
        flat_fields = {k: v for k, v in obj.items() if k not in _RESERVED_KEYS}
        if flat_fields:
            _extract_field_conditions(flat_fields, conditions)

    # Top-level scalar keys: time_range, limit, start_time
    for key in ("time_range", "limit", "start_time"):
        if key in obj:
            conditions.add(("__top__", key, str(obj[key]).lower()))

    # Top-level _or / _and clauses
    for clause_key in ("_or", "_and"):
        clause = obj.get(clause_key, [])
        if isinstance(clause, list):
            for item in clause:
                if isinstance(item, dict):
                    _extract_field_conditions(item, conditions)

    return conditions


def _op_compatible(expected_op: str, actual_op: str) -> bool:
    """Check if two operators are functionally compatible.

    The LLM may use _ilike with wildcards instead of _eq for exact values,
    or vice versa. Both are valid approaches for the same intent.
    ``_in`` is compatible with ``_eq`` (expanded list items match singles).
    """
    if expected_op == actual_op:
        return True
    # _ilike and _like are interchangeable
    if expected_op in _LIKE_OPS and actual_op in _LIKE_OPS:
        return True
    # _eq and _ilike/_like are compatible (LLM may choose either)
    if {expected_op, actual_op} & _EQ_OPS and {expected_op, actual_op} & _LIKE_OPS:
        return True
    # _in expanded items are compatible with _eq (and _ilike)
    if {expected_op, actual_op} & _LIST_OPS and {expected_op, actual_op} & (_EQ_OPS | _LIKE_OPS):
        return True
    return False


def _value_match(expected_val: str, actual_val: str) -> bool:
    """Fuzzy value comparison — strips wildcards and checks containment."""
    e = expected_val.strip("%").strip()
    a = actual_val.strip("%").strip()
    return e == a or e in a or a in e


def _match_conditions(
    source: Set[Tuple[str, str, str]],
    target: Set[Tuple[str, str, str]],
) -> int:
    """Count how many conditions in *source* have a match in *target*."""
    matched = 0
    for s_field, s_op, s_val in source:
        for t_field, t_op, t_val in target:
            if s_field == t_field and _op_compatible(s_op, t_op) and _value_match(s_val, t_val):
                matched += 1
                break
    return matched


def _compute_query_scores(
    expected_json: dict, actual_json: dict
) -> Tuple[float, float]:
    """Compute recall and precision (0-100) between expected and actual JSON.

    Returns:
        (recall, precision) where:
        - recall  (answer_similarity): fraction of expected conditions found
          in actual — did the agent cover everything requested?
        - precision (answer_relevancy): fraction of actual conditions that
          match expected — did the agent avoid hallucinating extra filters?
    """
    expected_conds = _extract_conditions(expected_json)
    actual_conds = _extract_conditions(actual_json)

    if not expected_conds and not actual_conds:
        return 100.0, 100.0
    if not expected_conds:
        return 100.0, 0.0
    if not actual_conds:
        return 0.0, 0.0

    recall_matched = _match_conditions(expected_conds, actual_conds)
    precision_matched = _match_conditions(actual_conds, expected_conds)

    recall = round((recall_matched / len(expected_conds)) * 100, 2)
    precision = round((precision_matched / len(actual_conds)) * 100, 2)
    return recall, precision


def _signoz_query_enricher(
    result: Dict[str, Any], test_case: Dict[str, Any], llm
):
    """Overwrite RAGAS scores with structural JSON comparison.

    RAGAS semantic similarity / relevancy don't work well for JSON filters
    (~73% even for correct outputs). This enricher replaces both with
    structural scores:

    - answer_similarity (recall): did the query cover all expected conditions?
    - answer_relevancy (precision): did the query avoid extra/hallucinated conditions?
    """
    actual_str = result.get("answer", "")
    expected_parts = test_case.get("expected_output", [])
    expected_str = expected_parts[0] if expected_parts else ""

    try:
        actual_json = json.loads(actual_str)
    except (json.JSONDecodeError, TypeError):
        logger.warning("Could not parse actual answer as JSON: %s", actual_str[:100])
        result["answer_similarity"] = 0.0
        result["answer_relevancy"] = 0.0
        return

    try:
        expected_json = json.loads(expected_str)
    except (json.JSONDecodeError, TypeError):
        logger.warning("Could not parse expected output as JSON: %s", expected_str[:100])
        return

    recall, precision = _compute_query_scores(expected_json, actual_json)
    result["answer_similarity"] = recall
    result["answer_relevancy"] = precision
    logger.info(
        "Query scores for %s: recall=%.1f%% precision=%.1f%%",
        test_case.get("__id__", "?"),
        recall,
        precision,
    )


# ---------------------------------------------------------------------------
# Benchmark entry point
# ---------------------------------------------------------------------------


@pytest.mark.benchmark
def test_signoz_logs_benchmark():
    """Run signoz log agent benchmark via LogDefaultAgent pipeline."""
    max_tests = int(os.getenv("MAX_TESTS", "0")) or None
    test_indices = os.getenv("TEST_INDICES")

    success = run_benchmark(
        agent=AGENT,
        tool_names=TOOL_NAMES,
        fixtures_dir=FIXTURES_DIR,
        answer_extractor=_extract_signoz_query,
        result_enricher=_signoz_query_enricher,
        max_tests=max_tests,
        test_indices=test_indices,
    )

    assert success, "Benchmark failed — no results stored"
    logger.info("Benchmark complete")
