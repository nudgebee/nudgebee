"""Did the investigation query the right host's metrics and logs?

A diagnosis can read well while resting on another machine's series, or on a
selector that matched nothing. This scores the queries the investigation
actually issued, read back from llm_conversation_tool_calls, against the
scenario's `expected_telemetry`:

    expected_telemetry:
      signals: [metrics, logs]        # which backends the investigation must use
      target_markers: ["${VM_A_INSTANCE}"]   # a scoped query contains one of these
      forbidden_markers: ["${VM_B_INSTANCE}"]  # no query may contain any of these
      service_markers: ["nb-bench-spin"]  # optional: a service-level alert also
                                          # needs a host-scoped query naming the unit

Markers are matched as substrings of the tool call's parameters, so they work
for any query syntax. For metrics the marker is whatever `instance` value the
platform resolves for the VM (node-agent hashes it), not the hostname.

A signal fails three separate ways, reported separately because each has a
different fix: never queried, queried without the target's marker, or queried
with it but returned nothing.
"""

from __future__ import annotations

import os
from typing import Any

from sqlalchemy import bindparam, create_engine, text

SIGNAL_TOOLS = {"metrics": "prometheus_execute", "logs": "logs_execute_v2"}

_EMPTY_RESPONSES = {"", "[]", "{}", "null"}

_engine = None


def _get_engine():
    global _engine
    url = os.environ.get("APP_DATABASE_URL", "")
    if _engine is None and url:
        _engine = create_engine(url, pool_size=2, max_overflow=2)
    return _engine


def event_resource_id(event_id: str) -> str | None:
    """The cloud resource (the VM) the platform linked an event to, or None when
    the database is not configured or the event has no resource."""
    engine = _get_engine()
    if engine is None:
        return None
    try:
        with engine.connect() as conn:
            row = conn.execute(
                text(
                    "SELECT CAST(cloud_resource_id AS text) FROM events WHERE id = :id"
                ),
                {"id": event_id},
            ).fetchone()
    except Exception:
        return None
    return row[0] if row and row[0] else None


def telemetry_calls(rca_response: dict[str, Any], account_id: str) -> list[dict] | None:
    """The metrics and logs tool calls made while investigating the event.

    Matches conversations by the event fingerprint the RCA response returns,
    which every session id for the event embeds. None when the database is not
    configured or reachable: that is "not measured", not "no queries".
    """
    fingerprint = rca_response.get("event_fingerprint")
    engine = _get_engine()
    if not fingerprint or engine is None:
        return None
    try:
        with engine.connect() as conn:
            rows = conn.execute(
                text("""
                    SELECT t.tool_name, t.parameters, t.status, t.response
                    FROM llm_conversation_tool_calls t
                    JOIN llm_conversations c ON c.id = t.conversation_id
                    WHERE CAST(c.account_id AS text) = :aid
                      AND c.session_id LIKE :pattern
                      AND t.tool_name IN :tools
                    ORDER BY t.created_at
                """).bindparams(bindparam("tools", expanding=True)),
                {
                    "aid": account_id,
                    "pattern": f"%{fingerprint}%",
                    "tools": list(SIGNAL_TOOLS.values()),
                },
            ).fetchall()
    except Exception:
        return None
    return [
        {
            "tool": r[0],
            "parameters": r[1] or "",
            "status": r[2] or "",
            "response": r[3] or "",
        }
        for r in rows
    ]


def _has_data(call: dict) -> bool:
    return (
        call["status"] == "success" and call["response"].strip() not in _EMPTY_RESPONSES
    )


def score_telemetry(expected: dict[str, Any], calls: list[dict]) -> dict[str, Any]:
    """Check `calls` against `expected_telemetry`. Pure, so it is unit-testable
    without a database."""
    targets = expected.get("target_markers") or []
    if not targets:
        raise ValueError("expected_telemetry.target_markers must name the target host")
    forbidden = expected.get("forbidden_markers") or []
    service_markers = expected.get("service_markers") or []

    checks: dict[str, dict[str, Any]] = {}
    for signal in expected.get("signals", []):
        tool = SIGNAL_TOOLS[signal]
        made = [c for c in calls if c["tool"] == tool]
        scoped = [c for c in made if any(m in c["parameters"] for m in targets)]
        checks[f"{signal}_queried"] = {
            "passed": bool(made),
            "detail": f"{len(made)} {tool} call(s)",
        }
        checks[f"{signal}_scoped_to_target"] = {
            "passed": bool(scoped),
            "detail": f"{len(scoped)} of {len(made)} call(s) carry a target marker",
        }
        if service_markers:
            by_service = [
                c for c in scoped if any(m in c["parameters"] for m in service_markers)
            ]
            checks[f"{signal}_scoped_to_service"] = {
                "passed": bool(by_service),
                "detail": f"{len(by_service)} of {len(scoped)} host-scoped call(s) "
                "also carry a service marker",
            }
        checks[f"{signal}_nonempty"] = {
            "passed": any(_has_data(c) for c in scoped),
            "detail": (
                "a target-scoped call returned data"
                if any(_has_data(c) for c in scoped)
                else "no target-scoped call returned data"
            ),
        }

    if forbidden:
        leaked = [c for c in calls if any(m in c["parameters"] for m in forbidden)]
        checks["no_other_host_queried"] = {
            "passed": not leaked,
            "detail": f"{len(leaked)} call(s) carry a forbidden marker",
        }

    return {"passed": all(c["passed"] for c in checks.values()), "checks": checks}
