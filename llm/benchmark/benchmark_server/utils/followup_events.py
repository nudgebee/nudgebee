"""Structured lifecycle logging for benchmark followups.

One JSON line per transition — queryable later via jq / log search.
Keeps a consistent schema across all call sites so dashboards and
stuck-state detection can be built on top.

Event schema:
    {
      "event": "followup.<state>",
      "run_id": "...",
      "test_index": 42,
      "conversation_id": "...",
      "followup_id": "...",       # message_id of the followup panel, if known
      "agent_id": "...",           # specific agent whose followup this is
      "followup_type": "tool_config",
      "from_state": "pending",
      "to_state": "waiting",
      "latency_ms": 1234,          # time since previous transition, when available
      "reason": "followup_timeout" # only populated on errors/timeouts
    }
"""

from __future__ import annotations

import json
import logging
import time
from typing import Any, Dict, Optional

logger = logging.getLogger("benchmark.followup")


def emit(
    event: str,
    *,
    run_id: str = "",
    test_index: Optional[int] = None,
    conversation_id: str = "",
    followup_id: str = "",
    agent_id: str = "",
    followup_type: str = "",
    from_state: str = "",
    to_state: str = "",
    latency_ms: Optional[int] = None,
    reason: str = "",
    extra: Optional[Dict[str, Any]] = None,
) -> None:
    """Emit one structured lifecycle line.

    Safe to call from anywhere — never raises. Unknown fields in ``extra``
    are merged at the top level for adhoc telemetry.
    """
    payload: Dict[str, Any] = {
        "event": f"followup.{event}" if not event.startswith("followup.") else event,
        "ts": time.time(),
    }
    if run_id:
        payload["run_id"] = run_id
    if test_index is not None:
        payload["test_index"] = test_index
    if conversation_id:
        payload["conversation_id"] = conversation_id
    if followup_id:
        payload["followup_id"] = followup_id
    if agent_id:
        payload["agent_id"] = agent_id
    if followup_type:
        payload["followup_type"] = followup_type
    if from_state:
        payload["from_state"] = from_state
    if to_state:
        payload["to_state"] = to_state
    if latency_ms is not None:
        payload["latency_ms"] = latency_ms
    if reason:
        payload["reason"] = reason
    if extra:
        for k, v in extra.items():
            if k not in payload:
                payload[k] = v
    try:
        logger.info(json.dumps(payload, default=str, separators=(",", ":")))
    except Exception:
        # Telemetry must never break the main flow.
        logger.info("followup event (unserializable): %s", payload)
