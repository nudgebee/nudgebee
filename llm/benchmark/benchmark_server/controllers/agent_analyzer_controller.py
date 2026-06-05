"""Agent Analyzer — inspect conversation execution flows, tool calls, and token usage."""

import logging
from typing import Optional

from fastapi import APIRouter, HTTPException, Query
from sqlalchemy import text

from benchmark_server.utils.db_utils import get_db

router = APIRouter(prefix="/agent-analyzer", tags=["agent-analyzer"])
logger = logging.getLogger(__name__)

NULL_PARENT = "00000000-0000-0000-0000-000000000000"


def _get_db_or_raise():
    db = get_db()
    if not db:
        raise HTTPException(status_code=503, detail="Database not configured")
    return db


@router.get("/filters")
def get_filters(account_id: Optional[str] = Query(None)):
    """Return distinct agent names and statuses for the filter dropdowns."""
    db = _get_db_or_raise()
    try:
        # Avoid f-string-built WHERE clauses: pick the full SQL up-front so a
        # future edit can't accidentally drop a bind param and re-introduce
        # injection through the dynamic predicate.
        params = {"null_parent": NULL_PARENT}
        if account_id:
            agents_sql = (
                "SELECT DISTINCT agent_name FROM llm_conversation_agent "
                "WHERE parent_agent_id = :null_parent AND account_id = :account_id "
                "ORDER BY agent_name"
            )
            statuses_sql = (
                "SELECT DISTINCT status FROM llm_conversation_agent "
                "WHERE parent_agent_id = :null_parent AND account_id = :account_id "
                "ORDER BY status"
            )
            params["account_id"] = account_id
        else:
            agents_sql = (
                "SELECT DISTINCT agent_name FROM llm_conversation_agent "
                "WHERE parent_agent_id = :null_parent "
                "ORDER BY agent_name"
            )
            statuses_sql = (
                "SELECT DISTINCT status FROM llm_conversation_agent "
                "WHERE parent_agent_id = :null_parent "
                "ORDER BY status"
            )

        agents = db.execute(text(agents_sql), params).fetchall()
        statuses = db.execute(text(statuses_sql), params).fetchall()
        return {
            "agent_names": [r[0] for r in agents if r[0]],
            "statuses": [r[0] for r in statuses if r[0]],
        }
    finally:
        db.close()


@router.get("/conversations")
def list_conversations(
    account_id: Optional[str] = Query(None),
    agent_name: Optional[str] = Query(None),
    status: Optional[str] = Query(None),
    query_search: Optional[str] = Query(None),
    limit: int = Query(20, ge=1, le=200),
):
    db = _get_db_or_raise()
    try:
        clauses = ["a.parent_agent_id = :null_parent"]
        params = {"limit": limit, "null_parent": NULL_PARENT}
        if account_id:
            clauses.append("a.account_id = :account_id")
            params["account_id"] = account_id
        if agent_name:
            clauses.append("a.agent_name = :agent_name")
            params["agent_name"] = agent_name
        if status:
            clauses.append("a.status = :status")
            params["status"] = status
        if query_search:
            clauses.append("a.query ILIKE :query_search")
            params["query_search"] = f"%{query_search}%"

        where = " AND ".join(clauses)
        rows = db.execute(
            text(f"""
                SELECT a.conversation_id, a.agent_name, a.status,
                    COALESCE(EXTRACT(EPOCH FROM (a.updated_at - a.created_at))::int, 0) AS duration_sec,
                    LEFT(a.query, 120) AS query_preview,
                    a.created_at,
                    COALESCE(c.title, '') AS title
                FROM llm_conversation_agent a
                LEFT JOIN llm_conversations c ON c.id = a.conversation_id
                WHERE {where}
                ORDER BY a.created_at DESC
                LIMIT :limit
            """),
            params,
        ).fetchall()

        return [
            {
                "id": str(r[0]),
                "agent_name": r[1],
                "status": r[2],
                "duration_sec": r[3] or 0,
                "query_preview": r[4] or "",
                "created_at": r[5].isoformat() if r[5] else None,
                "title": r[6] or "",
            }
            for r in rows
        ]
    finally:
        db.close()


@router.get("/agents")
def list_agents(conversation_id: str = Query(...)):
    db = _get_db_or_raise()
    try:
        rows = db.execute(
            text("""
                SELECT id, agent_name, status,
                    COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at))::int, 0) AS duration_sec,
                    COALESCE(parent_agent_id::text, '') AS parent_agent_id,
                    LEFT(query, 200) AS query_preview,
                    LEFT(COALESCE(response, ''), 500) AS response,
                    created_at, updated_at
                FROM llm_conversation_agent
                WHERE conversation_id = :cid
                ORDER BY created_at ASC
            """),
            {"cid": conversation_id},
        ).fetchall()

        return [
            {
                "id": str(r[0]),
                "agent_name": r[1],
                "status": r[2],
                "duration_sec": r[3] or 0,
                "parent_agent_id": str(r[4]) if r[4] else "",
                "query_preview": r[5] or "",
                "response": r[6] or "",
                "created_at": r[7].isoformat() if r[7] else None,
                "updated_at": r[8].isoformat() if r[8] else None,
            }
            for r in rows
        ]
    finally:
        db.close()


@router.get("/tool-calls")
def list_tool_calls(
    agent_id: Optional[str] = Query(None),
    conversation_id: Optional[str] = Query(None),
):
    if not agent_id and not conversation_id:
        raise HTTPException(
            status_code=400, detail="agent_id or conversation_id required"
        )

    db = _get_db_or_raise()
    try:
        if agent_id:
            where, params = "agent_id = :id", {"id": agent_id}
        else:
            where, params = "conversation_id = :id", {"id": conversation_id}

        rows = db.execute(
            text(f"""
                SELECT id, tool_name, agent_id,
                    COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at))::int, 0) AS duration_sec,
                    COALESCE(LEFT(thought, 300), '') AS thought,
                    COALESCE(LEFT(parameters, 300), '') AS parameters,
                    COALESCE(LEFT(response, 500), '') AS response,
                    COALESCE(status, '') AS status,
                    child_agent_id,
                    created_at, updated_at
                FROM llm_conversation_tool_calls
                WHERE {where}
                ORDER BY created_at ASC
            """),
            params,
        ).fetchall()

        return [
            {
                "id": str(r[0]),
                "tool_name": r[1],
                "agent_id": str(r[2]),
                "duration_sec": r[3] or 0,
                "thought": r[4] or "",
                "parameters": r[5] or "",
                "response": r[6] or "",
                "status": r[7] or "",
                "child_agent_id": str(r[8]) if r[8] else None,
                "created_at": r[9].isoformat() if r[9] else None,
                "updated_at": r[10].isoformat() if r[10] else None,
            }
            for r in rows
        ]
    finally:
        db.close()


@router.get("/token-usage")
def list_token_usage(conversation_id: str = Query(...)):
    db = _get_db_or_raise()
    try:
        rows = db.execute(
            text("""
                SELECT COALESCE(agent_id::text, '') AS agent_id,
                    COALESCE(agent_name, '') AS agent_name,
                    COALESCE(llm_provider, '') AS llm_provider,
                    COALESCE(llm_model, '') AS llm_model,
                    COALESCE(input_tokens, 0) AS input_tokens,
                    COALESCE(output_tokens, 0) AS output_tokens,
                    COALESCE(cached_input_tokens, 0) AS cached_input_tokens,
                    COALESCE(latency_seconds, 0)::float AS latency_seconds,
                    COALESCE(request_status, '') AS request_status,
                    COALESCE(stop_reason, '') AS stop_reason,
                    created_at
                FROM llm_conversation_token_usage
                WHERE conversation_id = :cid
                ORDER BY created_at ASC
            """),
            {"cid": conversation_id},
        ).fetchall()

        return [
            {
                "agent_id": r[0] or "",
                "agent_name": r[1] or "",
                "llm_provider": r[2] or "",
                "llm_model": r[3] or "",
                "input_tokens": r[4] or 0,
                "output_tokens": r[5] or 0,
                "cached_input_tokens": r[6] or 0,
                "latency_seconds": float(r[7] or 0),
                "request_status": r[8] or "",
                "stop_reason": r[9] or "",
                "created_at": r[10].isoformat() if r[10] else None,
            }
            for r in rows
        ]
    finally:
        db.close()


@router.get("/flow")
def get_flow(conversation_id: str = Query(...)):
    """Build the full execution tree for a conversation."""
    db = _get_db_or_raise()
    try:
        # Fetch all agents
        agent_rows = db.execute(
            text("""
                SELECT id, agent_name, status,
                    COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at))::int, 0),
                    COALESCE(parent_agent_id::text, ''),
                    COALESCE(LEFT(query, 300), ''),
                    COALESCE(LEFT(response, 1000), ''),
                    created_at, updated_at
                FROM llm_conversation_agent
                WHERE conversation_id = :cid
                ORDER BY created_at ASC
            """),
            {"cid": conversation_id},
        ).fetchall()

        agents = {}
        agent_order = []
        for r in agent_rows:
            aid = str(r[0])
            agents[aid] = {
                "id": aid,
                "agent_name": r[1],
                "status": r[2],
                "duration_sec": r[3] or 0,
                "parent_agent_id": str(r[4]) if r[4] else "",
                "query_preview": r[5] or "",
                "response": r[6] or "",
                "created_at": r[7].isoformat() if r[7] else None,
                "updated_at": r[8].isoformat() if r[8] else None,
            }
            agent_order.append(aid)

        # Fetch all tool calls
        tool_rows = db.execute(
            text("""
                SELECT id, tool_name, agent_id,
                    COALESCE(EXTRACT(EPOCH FROM (updated_at - created_at))::int, 0),
                    COALESCE(LEFT(thought, 500), ''),
                    COALESCE(LEFT(parameters, 500), ''),
                    COALESCE(LEFT(response, 1000), ''),
                    COALESCE(status, ''),
                    child_agent_id,
                    created_at, updated_at
                FROM llm_conversation_tool_calls
                WHERE conversation_id = :cid
                ORDER BY created_at ASC
            """),
            {"cid": conversation_id},
        ).fetchall()

        tool_calls_by_agent = {}
        for r in tool_rows:
            tc = {
                "id": str(r[0]),
                "tool_name": r[1],
                "agent_id": str(r[2]),
                "duration_sec": r[3] or 0,
                "thought": r[4] or "",
                "parameters": r[5] or "",
                "response": r[6] or "",
                "status": r[7] or "",
                "child_agent_id": str(r[8]) if r[8] else None,
                "created_at": r[9].isoformat() if r[9] else None,
                "updated_at": r[10].isoformat() if r[10] else None,
            }
            tool_calls_by_agent.setdefault(tc["agent_id"], []).append(tc)

        # Fetch all token usage
        token_rows = db.execute(
            text("""
                SELECT COALESCE(agent_id::text, ''),
                    COALESCE(agent_name, ''),
                    COALESCE(llm_provider, ''),
                    COALESCE(llm_model, ''),
                    COALESCE(input_tokens, 0),
                    COALESCE(output_tokens, 0),
                    COALESCE(cached_input_tokens, 0),
                    COALESCE(latency_seconds, 0)::float,
                    COALESCE(request_status, ''),
                    COALESCE(stop_reason, ''),
                    created_at
                FROM llm_conversation_token_usage
                WHERE conversation_id = :cid
                ORDER BY created_at ASC
            """),
            {"cid": conversation_id},
        ).fetchall()

        tokens_by_agent = {}
        for r in token_rows:
            tu = {
                "agent_id": r[0] or "",
                "agent_name": r[1] or "",
                "llm_provider": r[2] or "",
                "llm_model": r[3] or "",
                "input_tokens": r[4] or 0,
                "output_tokens": r[5] or 0,
                "cached_input_tokens": r[6] or 0,
                "latency_seconds": float(r[7] or 0),
                "request_status": r[8] or "",
                "stop_reason": r[9] or "",
                "created_at": r[10].isoformat() if r[10] else None,
            }
            tokens_by_agent.setdefault(tu["agent_id"], []).append(tu)

        # Build tree nodes
        nodes = {}
        for aid in agent_order:
            tcs = tool_calls_by_agent.get(aid, [])
            tus = tokens_by_agent.get(aid, [])
            totals = {
                "llm_calls": len(tus),
                "input_tokens": sum(t["input_tokens"] for t in tus),
                "output_tokens": sum(t["output_tokens"] for t in tus),
                "cached_tokens": sum(t["cached_input_tokens"] for t in tus),
                "total_latency": sum(t["latency_seconds"] for t in tus),
                "tool_call_count": len(tcs),
            }
            nodes[aid] = {
                "agent": agents[aid],
                "tool_calls": tcs,
                "token_usage": tus,
                "children": [],
                "totals": totals,
            }

        # Link parent-child
        roots = []
        for aid in agent_order:
            node = nodes[aid]
            parent_id = agents[aid]["parent_agent_id"]
            if not parent_id or parent_id == NULL_PARENT:
                roots.append(node)
            elif parent_id in nodes:
                nodes[parent_id]["children"].append(node)
            else:
                # Try to attach via tool_call.child_agent_id
                attached = False
                for pid, tcs in tool_calls_by_agent.items():
                    for tc in tcs:
                        if tc["child_agent_id"] == aid and pid in nodes:
                            nodes[pid]["children"].append(node)
                            attached = True
                            break
                    if attached:
                        break
                if not attached:
                    roots.append(node)

        # Also link children discovered via tool_call child_agent_id
        for tcs in tool_calls_by_agent.values():
            for tc in tcs:
                child_id = tc.get("child_agent_id")
                if not child_id or child_id not in nodes:
                    continue
                parent_id = agents[child_id]["parent_agent_id"]
                if parent_id and parent_id != NULL_PARENT:
                    continue  # already linked via parent_agent_id
                parent_node = nodes.get(tc["agent_id"])
                if not parent_node:
                    continue
                child_node = nodes[child_id]
                if any(c["agent"]["id"] == child_id for c in parent_node["children"]):
                    continue  # already a child
                parent_node["children"].append(child_node)
                roots = [r for r in roots if r["agent"]["id"] != child_id]

        return roots
    finally:
        db.close()
