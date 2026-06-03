"""Shared metrics collection for agent benchmarks.

Provides functions to fetch token usage, tool call info, and planner
responses from the LLM server and database.
"""

import json
import logging
import os
import sys

import requests
from dotenv import load_dotenv
from sqlalchemy import create_engine, text

from benchmark_server.utils.utils import Config

load_dotenv()

logger = logging.getLogger(__name__)

# --- Database Setup ---
_DB_URL = os.environ.get("APP_DATABASE_URL", "")
_db_engine = None
if _DB_URL:
    try:
        _db_engine = create_engine(_DB_URL, pool_size=2, max_overflow=5)
    except Exception as e:
        print(f"Warning: Failed to create database engine: {e}", file=sys.stderr)


def get_token_metrics(
    session_id: str, account_id: str, tenant_id: str, user_id: str
):
    """Fetch token usage, cost, and tool call metrics from the LLM server.

    Args:
        session_id: The LLM session ID (NOT the DB conversation UUID).
            The API payload field is named ``conversation_id`` but the
            Go server queries by ``session_id`` column internally.

    Returns a dict with total_tokens, input_tokens, completion_tokens,
    cached_input_tokens, cost, cache_hit_rate, model_providers, model_names,
    total_tool_calls, successful_tool_calls, timing info, etc.
    Returns None on failure.
    """
    if not session_id:
        return None

    url = f"{Config.llm_server_url}/v1/completions/conversation-usage-metrics"
    headers = {
        "x-tenant-id": tenant_id,
        "x-user-id": user_id,
        "Content-Type": "application/json",
    }
    # API field is named "conversation_id" but server queries by session_id
    payload = json.dumps(
        {
            "conversation_id": session_id,
            "account_id": account_id,
            "user_id": user_id,
        }
    )

    try:
        response = requests.post(url, headers=headers, data=payload)
        response.raise_for_status()
        data = response.json()

        if "data" not in data or "conversation" not in data["data"]:
            return None

        conv = data["data"]["conversation"]
        input_tokens = conv.get("total_input_tokens", 0)
        completion_tokens = conv.get("total_output_tokens", 0)
        cached_input_tokens = conv.get("total_cached_input_tokens", 0)

        model_providers = set()
        model_names = set()
        # Prefer top-level model_usage (new format)
        for mu in conv.get("model_usage", []):
            if mu.get("model_provider"):
                model_providers.add(mu["model_provider"])
            if mu.get("model_name"):
                model_names.add(mu["model_name"])
        # Fallback to messages[].agents[] (legacy nullable SQL format)
        if not model_names:
            for message in conv.get("messages", []):
                for agent in message.get("agents", []):
                    prov = agent.get("model_provider_name")
                    if isinstance(prov, dict) and prov.get("Valid"):
                        model_providers.add(prov["String"])
                    elif isinstance(prov, str) and prov:
                        model_providers.add(prov)
                    name = agent.get("model_name")
                    if isinstance(name, dict) and name.get("Valid"):
                        model_names.add(name["String"])
                    elif isinstance(name, str) and name:
                        model_names.add(name)

        return {
            "total_tokens": input_tokens + completion_tokens,
            "input_tokens": input_tokens,
            "completion_tokens": completion_tokens,
            "cached_input_tokens": cached_input_tokens,
            "cost": conv.get("total_cost_usd", 0.0),
            "cache_hit_rate": conv.get("total_cache_hit_rate_percentage", 0.0),
            "model_providers": sorted(list(model_providers)),
            "model_names": sorted(list(model_names)),
            "total_tool_calls": conv.get("total_tool_calls", 0),
            "successful_tool_calls": conv.get("successful_tool_calls", 0),
            "wall_time_seconds": conv.get("wall_time_seconds"),
            "tool_time_seconds": conv.get("tool_time_seconds"),
            "api_time_seconds": conv.get("api_time_seconds"),
            "success_rate_percentage": conv.get("success_rate_percentage"),
            "total_requests": conv.get("total_requests", 0),
            "failed_requests": conv.get("failed_requests", 0),
        }
    except requests.exceptions.RequestException as e:
        logger.error("Failed to get token metrics for session %s: %s", session_id, e)
    except json.JSONDecodeError as e:
        logger.error(
            "Failed to decode token metrics JSON for session %s: %s", session_id, e
        )

    return None


def get_planner_response(convo_id: str, account_id: str):
    """Fetch the planner agent's response from the database.

    Returns the planner's response text or None if not found.
    """
    if not convo_id or not _db_engine:
        return None

    try:
        with _db_engine.connect() as connection:
            query = text(
                """
                SELECT response
                FROM llm_conversation_agent
                WHERE conversation_id = :conversation_id
                  AND account_id = :account_id
                  AND agent_name = 'planner'
                  AND response IS NOT NULL
                ORDER BY created_at DESC
                LIMIT 1
            """
            )
            result = connection.execute(
                query, {"conversation_id": convo_id, "account_id": account_id}
            ).fetchone()

            if result and result[0]:
                return str(result[0])
            return None
    except Exception as e:
        logger.error("Failed to get planner response for %s: %s", convo_id, e)
        return None


def get_execution_trace(convo_id: str, account_id: str) -> str:
    """Build a structured execution trace from conversation DB for planner evaluation.

    Returns a text summary of:
    - Planner thought and plan
    - Every agent call with query, status, thought
    - Every tool call with params, status, thought, response summary
    - Execution stats (total/success/fail/error counts)
    """
    if not convo_id or not _db_engine:
        return ""

    try:
        with _db_engine.connect() as conn:
            # Planner thought + response
            planner = conn.execute(
                text("""
                    SELECT thought, response
                    FROM llm_conversation_agent
                    WHERE conversation_id = :cid AND account_id = :aid AND agent_name = 'planner'
                    ORDER BY created_at DESC LIMIT 1
                """),
                {"cid": convo_id, "aid": account_id},
            ).fetchone()

            # All agent calls (excluding planner and LLM helper)
            agents = conn.execute(
                text("""
                    SELECT agent_name, query, status, LEFT(thought, 300) as thought
                    FROM llm_conversation_agent
                    WHERE conversation_id = :cid
                      AND agent_name NOT IN ('planner', 'LLM')
                    ORDER BY created_at
                """),
                {"cid": convo_id},
            ).fetchall()

            # All tool calls
            tools = conn.execute(
                text("""
                    SELECT tool_name, parameters, status, LEFT(thought, 200) as thought,
                           LEFT(response, 300) as response
                    FROM llm_conversation_tool_calls
                    WHERE conversation_id = :cid
                    ORDER BY created_at
                """),
                {"cid": convo_id},
            ).fetchall()

        # Build trace summary
        parts = []

        # Plan section
        if planner:
            thought = (planner[0] or "")[:500]
            plan = (planner[1] or "")[:1500]
            parts.append(f"== PLAN ==\nThought: {thought}\nPlan: {plan}")

        # Agent calls section
        if agents:
            agent_lines = []
            for i, a in enumerate(agents, 1):
                name, query, status, thought = a[0], (a[1] or "")[:150], a[2] or "unknown", (a[3] or "")[:100]
                line = f"  Agent {i}: [{status.upper()}] {name}(\"{query}\")"
                if thought:
                    line += f"\n    Thought: {thought}"
                agent_lines.append(line)
            parts.append("== AGENT CALLS ==\n" + "\n".join(agent_lines))

        # Tool calls section
        if tools:
            tool_lines = []
            for i, t in enumerate(tools, 1):
                name, params, status, thought, resp = (
                    t[0], (t[1] or "")[:150], t[2] or "unknown",
                    (t[3] or "")[:100], (t[4] or "")[:150],
                )
                line = f"  Step {i}: [{status.upper()}] {name}({params})"
                if thought:
                    line += f"\n    Thought: {thought}"
                if status in ("error", "fail") and resp:
                    line += f"\n    Error: {resp}"
                elif status == "success" and resp:
                    # Truncate successful responses
                    line += f"\n    Result: {resp[:100]}..."
                tool_lines.append(line)
            parts.append("== TOOL CALLS ==\n" + "\n".join(tool_lines))

        # Stats
        if tools:
            total = len(tools)
            success = sum(1 for t in tools if t[2] == "success")
            failed = sum(1 for t in tools if t[2] == "fail")
            errors = sum(1 for t in tools if t[2] == "error")
            parts.append(
                f"== STATS ==\n"
                f"Total tool calls: {total} | Success: {success} | Failed: {failed} | Errors: {errors}"
            )

        return "\n\n".join(parts) if parts else ""

    except Exception as e:
        logger.error("Failed to build execution trace for %s: %s", convo_id, e)
        return ""


def get_tool_names(convo_id: str):
    """Fetch distinct tool names used in a conversation from the database.

    Returns dict with tool_names (successful) and tool_names_failed lists.
    """
    if not convo_id or not _db_engine:
        return None

    try:
        with _db_engine.connect() as connection:
            query = text(
                """
                SELECT DISTINCT tool_name, status
                FROM llm_conversation_tool_calls
                WHERE conversation_id = :conversation_id
                  AND tool_name IS NOT NULL
                  AND tool_name != ''
            """
            )
            rows = connection.execute(query, {"conversation_id": convo_id}).fetchall()

            tool_names = set()
            tool_names_failed = set()
            for row in rows:
                name, status = row[0], row[1]
                if status == "fail":
                    tool_names_failed.add(name)
                else:
                    tool_names.add(name)

            return {
                "tool_names": sorted(list(tool_names)),
                "tool_names_failed": sorted(list(tool_names_failed)),
            }
    except Exception as e:
        logger.error("Failed to get tool names for %s: %s", convo_id, e)
        return None


def get_failure_diagnostics(convo_id: str, account_id: str):
    """Fetch diagnostic info for a failed conversation from the database.

    Queries agent errors and tool call errors to build a diagnostic summary.
    Returns a dict with agent_error, tool_errors, and failed_tool_names,
    or None if no diagnostics are available.
    """
    if not convo_id or not _db_engine:
        return None

    try:
        diagnostics = {}

        with _db_engine.connect() as connection:
            # Get agent-level error (the main error message)
            agent_query = text(
                """
                SELECT agent_name, status, LEFT(response, 500) as response
                FROM llm_conversation_agent
                WHERE conversation_id = :conversation_id
                  AND account_id = :account_id
                  AND status = 'fail'
                ORDER BY created_at DESC
                LIMIT 1
            """
            )
            agent_result = connection.execute(
                agent_query, {"conversation_id": convo_id, "account_id": account_id}
            ).fetchone()

            if agent_result:
                diagnostics["agent_name"] = agent_result[0]
                diagnostics["agent_error"] = agent_result[2] or ""

            # Get tool-level errors
            tool_query = text(
                """
                SELECT tool_name, status, LEFT(response, 300) as response
                FROM llm_conversation_tool_calls
                WHERE conversation_id = :conversation_id
                  AND account_id = :account_id
                  AND status IN ('error', 'fail')
                ORDER BY created_at DESC
                LIMIT 5
            """
            )
            tool_results = connection.execute(
                tool_query, {"conversation_id": convo_id, "account_id": account_id}
            ).fetchall()

            if tool_results:
                diagnostics["tool_errors"] = [
                    {
                        "tool_name": row[0],
                        "status": row[1],
                        "error": row[2] or "",
                    }
                    for row in tool_results
                ]
                diagnostics["failed_tool_names"] = sorted(
                    set(row[0] for row in tool_results)
                )

        return diagnostics if diagnostics else None
    except Exception as e:
        logger.error("Failed to get failure diagnostics for %s: %s", convo_id, e)
        return None


def fetch_tool_command_from_db(conversation_id: str, account_id: str, tool_name: str):
    """Fetch the most recent tool command from the database.

    Checks params_sql first, then falls back to extracting the command
    from the parameters JSON column (e.g. {"command": "az account list"}).
    """
    if not conversation_id or not _db_engine:
        return None

    try:
        with _db_engine.connect() as connection:
            query = text(
                """
                SELECT params_sql, parameters
                FROM llm_conversation_tool_calls
                WHERE account_id = :account_id
                  AND conversation_id = :conversation_id
                  AND tool_name = :tool_name
                ORDER BY updated_at DESC
                LIMIT 1
            """
            )
            result = connection.execute(
                query,
                {
                    "account_id": account_id,
                    "conversation_id": conversation_id,
                    "tool_name": tool_name,
                },
            ).fetchone()

            if not result:
                return None

            # Prefer params_sql if available
            if result[0]:
                return str(result[0])

            # Fall back to parameters JSON (e.g. {"command": "az vm list ..."})
            if result[1]:
                try:
                    params = json.loads(result[1]) if isinstance(result[1], str) else result[1]
                    if isinstance(params, dict) and params.get("command"):
                        return str(params["command"])
                except (json.JSONDecodeError, TypeError):
                    pass

            return None
    except Exception as e:
        logger.error("Failed to fetch tool command from DB: %s", e)
        return None


def lookup_session_id(conversation_uuid: str):
    """Look up the session_id for a conversation given its DB UUID.

    Used as a fallback for old benchmark runs that stored conversation_id
    but not polling_conversation_id (session_id).
    """
    if not conversation_uuid or not _db_engine:
        return None

    try:
        with _db_engine.connect() as connection:
            query = text(
                """
                SELECT session_id
                FROM llm_conversations
                WHERE id = :conversation_id
                  AND session_id IS NOT NULL
                  AND session_id != ''
                LIMIT 1
            """
            )
            result = connection.execute(
                query, {"conversation_id": conversation_uuid}
            ).fetchone()

            if result and result[0]:
                return str(result[0])
            return None
    except Exception as e:
        logger.error(
            "Failed to look up session_id for conversation %s: %s",
            conversation_uuid, e,
        )
        return None
