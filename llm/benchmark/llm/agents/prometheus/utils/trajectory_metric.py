"""Trajectory-level metrics for the prometheus parent agent.

Augments per-result metrics with agent-quality signals inspired by:
  - PromAssistant (arXiv:2503.03114): MetricAcc — did the executed PromQL
    reference the ground-truth metric names?
  - TRAJECT-Bench (arXiv:2510.04550): tool selection accuracy + dependency
    satisfaction — did the agent stay within the allowed toolset and avoid
    forbidden routes?
  - Prod loop pathology observed in DB: agents call prometheus_execute 7+
    times with the same metric and cosmetic label tweaks when data is missing.

All signals are derived from llm_conversation_tool_calls so they reflect
what the agent actually did, not what it claimed in its summary.
"""

import json
import logging
import os
import re
from typing import Any, Dict, List, Optional, Set

logger = logging.getLogger(__name__)

# DB engine is built lazily so pure-function tests (loop detection, metric_acc)
# can import this module without sqlalchemy installed.
_DB_URL = os.environ.get("APP_DATABASE_URL", "")
_db_engine = None
_db_text = None


def _get_db():
    """Return (engine, text) or (None, None) if sqlalchemy/DB are unavailable."""
    global _db_engine, _db_text
    if _db_engine is not None:
        return _db_engine, _db_text
    if not _DB_URL:
        return None, None
    try:
        from sqlalchemy import create_engine, text  # type: ignore

        _db_engine = create_engine(_DB_URL, pool_size=2, max_overflow=5)
        _db_text = text
        return _db_engine, _db_text
    except Exception as e:
        logger.warning("trajectory_metric: DB unavailable (%s); trajectory checks will be skipped", e)
        return None, None


# Metric names start with a letter or underscore; identifiers also include digits and ':'.
_METRIC_TOKEN = re.compile(r"\b([a-zA-Z_:][a-zA-Z0-9_:]*)\s*(?:\{|\[|\()")

# Tools that are always considered safe in addition to the per-test expected_tools.
# These are utility tools the planner may invoke without it being a regression.
_SAFE_DEFAULT_TOOLS: Set[str] = {
    "context_compression",
    "visualizer",
    "metrics_labels_list",
}

# Loop threshold: more than this many prometheus_execute calls sharing the
# leading metric name signals the no-data retry pathology.
_LOOP_BUCKET_THRESHOLD = 3


def _fetch_tool_calls(conversation_id: str) -> List[Dict[str, Any]]:
    """Return ordered tool calls for the conversation."""
    engine, text_fn = _get_db()
    if not conversation_id or engine is None:
        return []
    try:
        with engine.connect() as conn:
            rows = conn.execute(
                text_fn(
                    """
                    SELECT tool_name, parameters, status
                    FROM llm_conversation_tool_calls
                    WHERE conversation_id = :cid
                    ORDER BY created_at ASC
                    """
                ),
                {"cid": conversation_id},
            ).fetchall()
        return [
            {"tool_name": r[0], "parameters": r[1] or "", "status": r[2] or ""}
            for r in rows
        ]
    except Exception as e:
        logger.warning("trajectory_metric: tool call fetch failed for %s: %s", conversation_id, e)
        return []


def _extract_promql(parameters: str) -> str:
    """Pull the PromQL string out of a prometheus_execute parameters JSON.

    The agent invokes the tool with either a bare string or {"command": "..."}.
    """
    if not parameters:
        return ""
    try:
        parsed = json.loads(parameters)
    except (ValueError, TypeError):
        return parameters
    if isinstance(parsed, dict):
        cmd = parsed.get("command") or parsed.get("query") or ""
        return str(cmd)
    return str(parsed)


def _leading_metric(promql: str) -> str:
    """Best-effort: return the first metric identifier in a PromQL expression.

    Used as a bucket key for loop detection — agents that retry the same query
    family with cosmetic label changes will share the same leading metric.
    """
    match = _METRIC_TOKEN.search(promql or "")
    if match:
        return match.group(1)
    # Bare-metric form (no labels/range): return the trimmed expression.
    cleaned = (promql or "").strip()
    return cleaned.split()[0] if cleaned else ""


def _metric_acc(executed_promqls: List[str], expected_metrics: List[str]) -> float:
    """Fraction of expected_metrics that appear in any executed PromQL.

    Mirrors PromAssistant's MetricAcc but per-fixture instead of per-corpus.
    Returns 1.0 when no metrics are required (test asserts other dimensions).
    """
    if not expected_metrics:
        return 1.0
    if not executed_promqls:
        return 0.0
    haystack = " ".join(executed_promqls)
    hits = sum(1 for m in expected_metrics if m in haystack)
    return hits / len(expected_metrics)


def _detect_loop(executed_promqls: List[str]) -> bool:
    """True if any leading-metric bucket holds more than _LOOP_BUCKET_THRESHOLD calls.

    Catches the prod failure where the agent issues 7+ prometheus_execute calls
    against the same metric with only label tweaks after each returns no data.
    """
    buckets: Dict[str, int] = {}
    for q in executed_promqls:
        key = _leading_metric(q)
        if not key:
            continue
        buckets[key] = buckets.get(key, 0) + 1
    return any(count > _LOOP_BUCKET_THRESHOLD for count in buckets.values())


def enrich(
    result: Dict[str, Any],
    test_case: Dict[str, Any],
    _llm: Any = None,
) -> None:
    """Attach trajectory metrics to `result` in place.

    Adds:
      metric_acc                : float in [0,1]  — PromAssistant-style metric accuracy
      tool_selection_score      : float in [0,1]  — fraction of calls inside allowed set
      forbidden_tools_called    : List[str]       — regression signal
      expected_tools_used       : bool            — every entry of expected_tools fired
      tool_calls_within_budget  : bool            — total <= expected_max_tool_calls
      loop_detected             : bool            — same metric retried 4+ times
      executed_promqls          : List[str]       — for debugging / report
    """
    convo_id = result.get("conversation_id") or result.get("conversationId")
    # Runner stores convo_id elsewhere; recover from the planner trace fetch path.
    # store_test_result passes convo via the result dict only when present; fall
    # back to leaving signals empty if we cannot fetch tool calls.
    if not convo_id:
        # Last resort: scan execution_trace string for a UUID isn't reliable;
        # leave fields unset rather than guess.
        result.setdefault("metric_acc", None)
        result.setdefault("tool_selection_score", None)
        result.setdefault("loop_detected", None)
        return

    tool_calls = _fetch_tool_calls(convo_id)
    tool_names_called = [tc["tool_name"] for tc in tool_calls if tc.get("tool_name")]

    executed_promqls = [
        _extract_promql(tc["parameters"])
        for tc in tool_calls
        if tc["tool_name"] == "prometheus_execute"
    ]
    executed_promqls = [q for q in executed_promqls if q]

    expected_metrics: List[str] = test_case.get("expected_metrics") or []
    expected_tools: List[str] = test_case.get("expected_tools") or []
    forbidden_tools: List[str] = test_case.get("forbidden_tools") or []
    max_tool_calls: Optional[int] = test_case.get("expected_max_tool_calls")

    # 1) MetricAcc
    result["metric_acc"] = round(_metric_acc(executed_promqls, expected_metrics), 3)

    # 2) Tool selection — fraction of calls that landed in expected ∪ safe defaults.
    allowed: Set[str] = set(expected_tools) | _SAFE_DEFAULT_TOOLS
    if tool_names_called:
        in_allowed = sum(1 for n in tool_names_called if n in allowed)
        result["tool_selection_score"] = round(in_allowed / len(tool_names_called), 3)
    else:
        result["tool_selection_score"] = 0.0

    # 3) Forbidden tools — hard regression signal.
    forbidden_set = set(forbidden_tools)
    forbidden_hits = sorted({n for n in tool_names_called if n in forbidden_set})
    result["forbidden_tools_called"] = forbidden_hits

    # 4) Expected tools coverage.
    if expected_tools:
        called_set = set(tool_names_called)
        result["expected_tools_used"] = all(t in called_set for t in expected_tools)
    else:
        result["expected_tools_used"] = True

    # 5) Tool-call budget.
    if max_tool_calls is not None:
        result["tool_calls_within_budget"] = len(tool_calls) <= max_tool_calls
        result["tool_call_overage"] = max(0, len(tool_calls) - max_tool_calls)
    else:
        result["tool_calls_within_budget"] = True
        result["tool_call_overage"] = 0

    # 6) Loop detection.
    result["loop_detected"] = _detect_loop(executed_promqls)

    # Useful for failure triage in reports — keep small.
    result["executed_promqls"] = executed_promqls[:10]


def passed_trajectory_gates(result: Dict[str, Any], test_case: Dict[str, Any]) -> bool:
    """Hard pass/fail across all trajectory gates a fixture declared.

    Used by report tooling and CI assertions. A fixture passes the trajectory
    layer iff every declared gate (forbidden_tools, expected_tools, max_calls)
    is satisfied. metric_acc is informational, not a gate.
    """
    if result.get("forbidden_tools_called"):
        return False
    if test_case.get("expected_tools") and not result.get("expected_tools_used", False):
        return False
    if test_case.get("expected_max_tool_calls") is not None and not result.get(
        "tool_calls_within_budget", True
    ):
        return False
    if result.get("loop_detected"):
        return False
    return True
