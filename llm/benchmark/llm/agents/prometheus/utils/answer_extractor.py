"""Custom answer extractor for the prometheus parent agent benchmark.

The shared default returns a single tool command — fine for sub-agents that
emit one query, wrong for the parent agent which routinely runs multiple
prometheus_execute calls (e.g. CPU + memory + latency in one ask).

This extractor returns:
  1. Every PromQL string executed via prometheus_execute, in order
  2. The agent's final natural-language response (covers refusal / no-data
     fixtures where ground truth is a sentence, not a query)

Source priority for the PromQL list:
  - Primary  : in-response agent_step_response — works without DB access
               and survives env mismatches (no APP_DATABASE_URL, account_id
               drift, etc.).
  - Fallback : DB query — fills the list if agent_step_response is empty
               (some response shapes only carry the final text).
"""

import json
import logging
import os
from typing import List, Optional

from benchmark_server.utils.llm_client import (
    extract_conversation_id,
    extract_response_text,
)

logger = logging.getLogger(__name__)

# Cap the number of executed queries we surface to avoid prompt bloat in
# pathological loop-trap runs (the worst observed produced 25 calls).
_MAX_QUERIES = 10

_DB_URL = os.environ.get("APP_DATABASE_URL", "")
_db_engine = None
_db_text = None


def _get_db():
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
        logger.warning("answer_extractor: DB unavailable (%s)", e)
        return None, None


def _coerce_promql(raw: object) -> str:
    """Pull the PromQL string out of a tool-call argument blob.

    The agent passes args as either a JSON object ({"command": "..."}) or a
    bare PromQL string. Both shapes appear in real conversations.
    """
    if raw is None:
        return ""
    if isinstance(raw, dict):
        return str(raw.get("command") or raw.get("query") or "")
    if isinstance(raw, str):
        s = raw.strip()
        if not s:
            return ""
        # JSON-encoded string?
        if s.startswith("{"):
            try:
                parsed = json.loads(s)
            except (ValueError, TypeError):
                return s
            return _coerce_promql(parsed)
        return s
    return str(raw)


def _scan_response_for_promql(response_data: dict) -> List[str]:
    """Pull every prometheus_execute call argument out of agent_step_response.

    Mirrors the structural walk used by extract_tool_command in llm_client.py
    but returns ALL matches in order (the shared helper returns only the first).
    Works without DB access — preferred path for benchmark environments where
    APP_DATABASE_URL is not configured.
    """
    if not response_data:
        return []
    data = response_data.get("data", {}) or {}
    steps = data.get("agent_step_response", []) or []
    queries: List[str] = []
    for step in steps:
        call = (
            (step or {})
            .get("Call", {})
            .get("tool_call", {})
            .get("function", {})
        )
        if call.get("name") != "prometheus_execute":
            continue
        cmd = _coerce_promql(call.get("arguments", ""))
        if cmd:
            queries.append(cmd)
    return queries


def _fetch_all_promql(conversation_id: str, account_id: str) -> List[str]:
    """DB fallback: every prometheus_execute command in the conversation, in order.

    Always scopes to the given account_id when it is provided. If account_id is
    empty/None we run an unscoped lookup — the conversation_id alone is enough
    to identify the row in benchmark contexts where ACCOUNT_ID is not set, and
    omitting the filter on a non-empty account_id would risk cross-account
    leakage.
    """
    engine, text_fn = _get_db()
    if not conversation_id or engine is None:
        return []

    if account_id:
        query = """
            SELECT parameters
            FROM llm_conversation_tool_calls
            WHERE conversation_id = :cid
              AND account_id = :aid
              AND tool_name = 'prometheus_execute'
            ORDER BY created_at ASC
        """
        params = {"cid": conversation_id, "aid": account_id}
    else:
        query = """
            SELECT parameters
            FROM llm_conversation_tool_calls
            WHERE conversation_id = :cid
              AND tool_name = 'prometheus_execute'
            ORDER BY created_at ASC
        """
        params = {"cid": conversation_id}

    try:
        with engine.connect() as conn:
            rows = conn.execute(text_fn(query), params).fetchall()
    except Exception as e:
        logger.warning("answer_extractor: prometheus_execute DB fetch failed: %s", e)
        return []

    return [q for q in (_coerce_promql(r[0]) for r in rows if r[0]) if q]


def extract(
    response_data: dict,
    account_id: str,
    _tool_names: List[str],
    _db_tool_name: Optional[str] = None,
) -> List[str]:
    """Return [promql_1, ..., promql_n, final_response_text].

    Empty list when neither tool calls nor a response are recoverable —
    matches the default extractor's contract for failed conversations.
    """
    if not response_data:
        return []

    # Primary: scan the response itself — DB-free, always available.
    queries = _scan_response_for_promql(response_data)
    source = "agent_step_response"

    # Fallback: DB. Some response shapes don't include agent_step_response
    # (e.g. older async-poll responses); the DB has the full record.
    if not queries:
        convo_id = extract_conversation_id(response_data)
        if convo_id:
            queries = _fetch_all_promql(convo_id, account_id)
            source = "db" if queries else "none"

    if len(queries) > _MAX_QUERIES:
        queries = queries[:_MAX_QUERIES]

    response_text = extract_response_text(response_data) or ""
    if isinstance(response_text, list):
        response_text = " ".join(str(p) for p in response_text)
    response_text = response_text.strip()

    logger.debug(
        "answer_extractor: source=%s queries=%d response_len=%d",
        source, len(queries), len(response_text),
    )

    out: List[str] = list(queries)
    if response_text:
        out.append(response_text)
    return out
