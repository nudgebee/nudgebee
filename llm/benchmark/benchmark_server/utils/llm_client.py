import json
import logging
import os
import time
from dataclasses import dataclass
from typing import Optional

import requests

from benchmark_server.utils.utils import Config

logger = logging.getLogger(__name__)

TERMINAL_STATUSES = ("COMPLETED", "FAILED", "KILLED", "TERMINATED", "WAITING")


# --- Error categories ---
class ErrorCategory:
    """Classifies failures for retry decisions and reporting."""

    NETWORK = "network_error"  # connection refused, DNS, etc. — retryable
    TIMEOUT = "timeout"  # poll deadline exceeded — retryable
    SERVER_ERROR = "server_error"  # HTTP 5xx — retryable
    AGENT_FAILED = "agent_failed"  # agent ran but errored — NOT retryable
    AGENT_KILLED = "agent_killed"  # agent was killed/terminated — NOT retryable
    CLIENT_ERROR = "client_error"  # HTTP 4xx — NOT retryable
    EMPTY_RESPONSE = "empty_response"  # no conversation_id or data — NOT retryable

    RETRYABLE = {NETWORK, TIMEOUT, SERVER_ERROR}


@dataclass
class LLMResult:
    """Structured result from call_llm(), replaces raw dict/None returns."""

    data: Optional[dict] = None  # response dict (same shape as before)
    error_category: Optional[str] = None  # ErrorCategory constant
    error_message: Optional[str] = None  # human-readable error detail
    conversation_id: Optional[str] = None  # extracted even on failure
    status: Optional[str] = None  # COMPLETED, FAILED, KILLED, TERMINATED, WAITING
    # Unified followup representation: always a list (possibly empty),
    # one entry per pending followup panel. Preserved as the canonical
    # format in orchestrator storage and UI rendering.
    followups: Optional[list] = None
    # Legacy aliases — derived from ``followups``. Kept so callers that
    # haven't been migrated (e.g. older result serialization paths) keep
    # working. Prefer ``followups`` in new code.
    followup_request: Optional[dict] = None  # first followup
    followup_requests: Optional[list] = None  # all followups when >1 exists

    @property
    def success(self) -> bool:
        return self.data is not None and self.error_category is None

    @property
    def retryable(self) -> bool:
        return self.error_category in ErrorCategory.RETRYABLE


class LLMClientError(Exception):
    pass


try:
    LLM_POLL_TIMEOUT = int(os.environ.get("BENCHMARK_LLM_POLL_TIMEOUT", "1800"))
except (ValueError, TypeError):
    LLM_POLL_TIMEOUT = 300
try:
    LLM_POLL_INTERVAL = int(os.environ.get("BENCHMARK_LLM_POLL_INTERVAL", "30"))
except (ValueError, TypeError):
    LLM_POLL_INTERVAL = 30


def call_llm(
    query,
    account_id,
    tenant_id,
    user_id,
    config=None,
    conversation_id=None,
    agent_id=None,
    message_id=None,
    timeout=LLM_POLL_TIMEOUT,
    poll_interval=LLM_POLL_INTERVAL,
) -> LLMResult:
    """
    Call the LLM server. Uses async mode by default (sends ``async: true``,
    polls ``/v1/completions/chat_get`` for results).  Falls back to sync mode
    when ``BENCHMARK_ASYNC=false`` env-var is set.

    Returns an ``LLMResult`` with structured error classification.
    On success, ``result.data`` contains the response dict::

        {"data": {"response": [...], "agent_step_response": [...],
                  "conversation_id": "...", "session_id": "..."}}
    """
    use_async = os.getenv("BENCHMARK_ASYNC", "true").lower() != "false"

    url = Config.llm_server_url + "/v1/completions/chat"
    payload = {
        "query": query,
        "account_id": account_id,
        "tenant_id": tenant_id,
        "user_id": user_id,
    }
    if use_async:
        payload["async"] = True
    if config:
        payload["config"] = config
    if conversation_id:
        payload["conversation_id"] = conversation_id
    if agent_id:
        payload["agent_id"] = agent_id
    if message_id:
        payload["message_id"] = message_id

    headers = {
        "x-tenant-id": tenant_id,
        "x-user-id": user_id,
        "Content-Type": "application/json",
    }
    action_token = os.getenv("ACTION_TOKEN", "")
    if action_token:
        headers["x-action-token"] = action_token

    logger.info(
        "Sending request to %s (async=%s, LLM_SERVER_URL=%s)",
        url,
        use_async,
        Config.llm_server_url,
    )

    try:
        read_timeout = int(os.getenv("LLM_READ_TIMEOUT", "30"))
        response = requests.post(
            url, headers=headers, data=json.dumps(payload), timeout=read_timeout
        )
    except requests.RequestException as e:
        logger.error("Request to LLM server failed: %s", e)
        return LLMResult(
            error_category=ErrorCategory.NETWORK,
            error_message=f"Request to LLM server failed: {e}",
        )

    logger.info(
        "LLM response: status=%d, body=%s",
        response.status_code,
        response.text[:300],
    )

    # Sync path — server returned the result directly
    if response.status_code == 200:
        resp_data = response.json()
        return LLMResult(
            data=resp_data,
            conversation_id=_extract_convo_id(resp_data),
            status="COMPLETED",
        )

    # Async path — 202 accepted, need to poll
    if response.status_code == 202:
        return _poll_for_result(
            response.json(), account_id, tenant_id, user_id, timeout, poll_interval
        )

    # HTTP errors
    error_msg = (
        f"LLM server returned status {response.status_code}: " f"{response.text[:500]}"
    )
    logger.error(error_msg)

    if response.status_code >= 500:
        return LLMResult(
            error_category=ErrorCategory.SERVER_ERROR,
            error_message=error_msg,
        )
    return LLMResult(
        error_category=ErrorCategory.CLIENT_ERROR,
        error_message=error_msg,
    )


def fetch_conversation(
    conversation_id: str,
    account_id: str,
    tenant_id: str,
    user_id: str,
) -> LLMResult:
    """One-shot read of conversation state.

    Returns LLMResult with ``status`` set. No polling — used by reconciliation
    paths that just need the current snapshot. On error returns an LLMResult
    with ``error_category`` populated and ``status`` None.
    """
    poll_url = Config.llm_server_url + "/v1/completions/chat_get"
    headers = {
        "x-tenant-id": tenant_id or "",
        "x-user-id": user_id or "",
        "Content-Type": "application/json",
    }
    action_token = os.getenv("ACTION_TOKEN", "")
    if action_token:
        headers["x-action-token"] = action_token
    try:
        resp = requests.post(
            poll_url,
            headers=headers,
            data=json.dumps(
                {"conversation_id": conversation_id, "account_id": account_id or ""}
            ),
            timeout=int(os.getenv("LLM_READ_TIMEOUT", "30")),
        )
    except requests.RequestException as e:
        return LLMResult(
            conversation_id=conversation_id,
            error_category=ErrorCategory.NETWORK,
            error_message=str(e),
        )
    if resp.status_code != 200:
        return LLMResult(
            conversation_id=conversation_id,
            error_category=ErrorCategory.SERVER_ERROR,
            error_message=f"HTTP {resp.status_code}",
        )
    conv = resp.json().get("data")
    if not conv:
        return LLMResult(
            conversation_id=conversation_id,
            error_category=ErrorCategory.EMPTY_RESPONSE,
            error_message="chat_get returned no data",
        )
    status = conv.get("status", "") or ""
    transformed = _transform_conversation_response(conv)
    followups: list = []
    if status == "WAITING":
        try:
            followups = _extract_all_followups(conv) or []
        except Exception:
            followups = []
        if not followups:
            single = None
            try:
                single = _extract_followup_data(conv)
            except Exception:
                single = None
            if single:
                followups = [single]
    error_msg = ""
    error_cat = None
    if status in ("FAILED", "KILLED", "TERMINATED"):
        error_msg = (
            _extract_agent_error(conv) or f"Agent finished with status: {status}"
        )
        error_cat = (
            ErrorCategory.AGENT_KILLED
            if status in ("KILLED", "TERMINATED")
            else ErrorCategory.AGENT_FAILED
        )
    return LLMResult(
        data=transformed,
        conversation_id=conversation_id,
        status=status,
        followups=followups,
        followup_request=followups[0] if followups else None,
        followup_requests=followups if len(followups) > 1 else None,
        error_category=error_cat,
        error_message=error_msg or None,
    )


def _poll_for_result(
    initial_response, account_id, tenant_id, user_id, timeout, poll_interval
) -> LLMResult:
    """Poll ``/v1/completions/chat_get`` until the conversation reaches a terminal status."""
    data = initial_response.get("data", {})
    conversation_id = data.get("conversation_id")
    if not conversation_id:
        logger.error("No conversation_id in async response: %s", initial_response)
        return LLMResult(
            error_category=ErrorCategory.EMPTY_RESPONSE,
            error_message="No conversation_id in async response",
        )

    poll_url = Config.llm_server_url + "/v1/completions/chat_get"
    headers = {
        "x-tenant-id": tenant_id,
        "x-user-id": user_id,
        "Content-Type": "application/json",
    }
    action_token = os.getenv("ACTION_TOKEN", "")
    if action_token:
        headers["x-action-token"] = action_token
    deadline = time.time() + timeout

    logger.info(
        "Polling conversation %s (timeout=%ds, interval=%ds)",
        conversation_id,
        timeout,
        poll_interval,
    )

    while time.time() < deadline:
        time.sleep(poll_interval)
        try:
            poll_resp = requests.post(
                poll_url,
                headers=headers,
                data=json.dumps(
                    {
                        "conversation_id": conversation_id,
                        "account_id": account_id,
                    }
                ),
                timeout=int(os.getenv("LLM_READ_TIMEOUT", "30")),
            )
        except requests.RequestException as e:
            logger.warning("Poll request failed (will retry): %s", e)
            continue

        if poll_resp.status_code != 200:
            logger.warning(
                "Poll returned status %d (will retry)", poll_resp.status_code
            )
            continue

        conv = poll_resp.json().get("data")
        if not conv:
            continue

        status = conv.get("status", "")
        elapsed = int(time.time() - (deadline - timeout))
        if status not in TERMINAL_STATUSES:
            logger.info(
                "Polling %s: status=%s (%ds elapsed)", conversation_id, status, elapsed
            )
        if status in TERMINAL_STATUSES:
            logger.info("Conversation %s reached status %s", conversation_id, status)
            transformed = _transform_conversation_response(conv)

            if status == "WAITING":
                followups = []
                try:
                    followups = _extract_all_followups(conv)
                except Exception as e:
                    logger.error("Failed to extract followups: %s", e, exc_info=True)
                # Fall back to legacy single-followup extractor if the list
                # was empty for some reason (shouldn't happen in practice).
                if not followups:
                    try:
                        single = _extract_followup_data(conv)
                    except Exception as e:
                        logger.error(
                            "Failed to extract followup data: %s", e, exc_info=True
                        )
                        single = None
                    if single:
                        followups = [single]
                logger.info(
                    "Conversation %s WAITING for %d followup(s): %s",
                    conversation_id,
                    len(followups),
                    followups[0].get("question", "") if followups else "unknown",
                )
                return LLMResult(
                    data=transformed,
                    conversation_id=conversation_id,
                    status=status,
                    followups=followups,
                    # Legacy aliases — populated for backward-compat readers.
                    followup_request=followups[0] if followups else None,
                    followup_requests=followups if len(followups) > 1 else None,
                )

            if status == "COMPLETED":
                return LLMResult(
                    data=transformed,
                    conversation_id=conversation_id,
                    status=status,
                )

            # FAILED, KILLED, TERMINATED — extract error from agent response
            error_msg = _extract_agent_error(conv)
            error_cat = (
                ErrorCategory.AGENT_KILLED
                if status in ("KILLED", "TERMINATED")
                else ErrorCategory.AGENT_FAILED
            )
            return LLMResult(
                data=transformed,
                error_category=error_cat,
                error_message=error_msg or f"Agent finished with status: {status}",
                conversation_id=conversation_id,
                status=status,
            )

    logger.error("Polling timed out for conversation %s", conversation_id)
    return LLMResult(
        error_category=ErrorCategory.TIMEOUT,
        error_message=f"Polling timed out after {timeout}s for conversation {conversation_id}",
        conversation_id=conversation_id,
    )


def _extract_agent_error(conv: dict) -> str:
    """Extract the error message from a failed conversation's agent responses."""
    messages = conv.get("llm_conversation_messages", [])
    if not messages:
        return ""
    last_message = messages[-1]
    for agent in last_message.get("llm_conversation_agents", []):
        status = agent.get("status", "")
        if status == "fail":
            resp = agent.get("response", "")
            if resp:
                return resp[:500]
    return ""


def _extract_all_followups(conv: dict) -> list:
    """Extract ALL pending followup questions from a WAITING conversation.
    Returns a list of followup dicts, one per pending followup message.
    Each followup gets the correct agent_id by matching the agent whose
    followup_message_id points to this specific followup message."""
    messages = conv.get("llm_conversation_messages", [])
    followup_msgs = [
        m
        for m in messages
        if m.get("message_type") == "followup" and m.get("status") != "COMPLETED"
    ]

    # Build a map: followup_message_id → agent (from the generation message's agents)
    # Each agent's followup_message_id points to the followup it created.
    followup_to_agent = {}
    generation_message_id = ""
    for m in messages:
        if m.get("message_type") != "followup":
            generation_message_id = str(m.get("id", ""))
            for agent in m.get("llm_conversation_agents") or []:
                fmid = str(agent.get("followup_message_id", ""))
                if fmid and fmid != "00000000-0000-0000-0000-000000000000":
                    followup_to_agent[fmid] = agent

    results = []
    for msg in followup_msgs:
        msg_id = str(msg.get("id", ""))
        config_str = msg.get("message_config")
        if not config_str:
            continue
        try:
            config = (
                json.loads(config_str) if isinstance(config_str, str) else config_str
            )
        except (json.JSONDecodeError, TypeError):
            continue

        # Find the agent that owns this followup
        agent = followup_to_agent.get(msg_id, {})
        agent_id = str(agent.get("id", ""))
        # The message_id for submitting is the generation message, not the followup message
        resolved_message_id = generation_message_id

        # Fallback: find top-level waiting agent if no direct match
        if not agent_id:
            ZERO_UUID = "00000000-0000-0000-0000-000000000000"
            for m in reversed(messages):
                if m.get("message_type") == "followup":
                    continue
                for a in m.get("llm_conversation_agents") or []:
                    if (
                        str(a.get("status", "")).lower() == "waiting"
                        and str(a.get("parent_agent_id", "")) == ZERO_UUID
                    ):
                        agent_id = str(a.get("id", ""))
                        resolved_message_id = str(m.get("id", ""))
                        break
                if agent_id:
                    break

        results.append(
            {
                "question": config.get("question", msg.get("message", "")),
                "followup_type": config.get("followupType", "text"),
                "options": config.get("followupOptions", []),
                "data": config.get("followupData"),
                "agent_id": agent_id,
                "agent_name": config.get("agentName", ""),
                "message_id": resolved_message_id,
                "tool_name": config.get("toolName", ""),
            }
        )
    return results


def _extract_followup_data(conv: dict) -> Optional[dict]:
    """Extract followup question and agent_id from a WAITING conversation."""
    messages = conv.get("llm_conversation_messages", [])
    if not messages:
        logger.warning("followup: no messages in conversation")
        return None

    # Log message types for debugging
    msg_types = [
        (
            m.get("message_type"),
            m.get("status"),
            len(m.get("llm_conversation_agents") or []),
        )
        for m in messages
    ]
    logger.info("followup: conversation has %d messages: %s", len(messages), msg_types)

    # Find followup messages — prefer the last unanswered (non-COMPLETED) one.
    # Multiple followups can exist when parallel tool calls each trigger a tool_config prompt.
    followup_msgs = [m for m in messages if m.get("message_type") == "followup"]
    if not followup_msgs:
        logger.warning("followup: no message with message_type='followup' found")
        return None

    if len(followup_msgs) > 1:
        logger.warning(
            "followup: found %d followup messages (possible duplicate tool_config)",
            len(followup_msgs),
        )

    # Only consider uncompleted followups. Falling back to a COMPLETED
    # followup would surface stale data — observed when the llm-server is
    # in a zombie WAITING state (parent agent still running internally
    # after all user-facing followups have been answered). Returning None
    # here lets ``fetch_conversation``/reconcile treat the state as
    # "in-flight" rather than rendering a phantom input form.
    followup_msg = None
    for msg in reversed(followup_msgs):
        if msg.get("status") != "COMPLETED":
            followup_msg = msg
            break
    if not followup_msg:
        return None

    # Parse message_config for followup details
    config_str = followup_msg.get("message_config")
    if not config_str:
        logger.warning("followup: followup message has no message_config")
        return None
    try:
        config = json.loads(config_str) if isinstance(config_str, str) else config_str
    except (json.JSONDecodeError, TypeError) as e:
        logger.warning("followup: failed to parse message_config: %s", e)
        return None

    # Find the top-level waiting agent and message_id.
    # The UI sends the parent (top-level) agent_id and the generation message_id.
    # The top-level agent has parent_agent_id == zero UUID.
    ZERO_UUID = "00000000-0000-0000-0000-000000000000"
    agent_id = None
    message_id = None
    for msg in reversed(messages):
        if msg.get("message_type") == "followup":
            continue
        msg_agents = msg.get("llm_conversation_agents") or []
        for agent in msg_agents:
            agent_status = str(agent.get("status", "")).lower()
            parent = str(agent.get("parent_agent_id", ""))
            if agent_status == "waiting" and parent == ZERO_UUID:
                agent_id = str(agent.get("id", ""))
                message_id = str(msg.get("id", ""))
                break
        if agent_id:
            break

    # Fallback: if no top-level waiting agent found, use any waiting agent
    if not agent_id:
        for msg in reversed(messages):
            for agent in msg.get("llm_conversation_agents") or []:
                if str(agent.get("status", "")).lower() == "waiting":
                    agent_id = str(agent.get("id", ""))
                    message_id = str(msg.get("id", ""))
                    break
            if agent_id:
                break

    if not agent_id:
        logger.warning("followup: no agent with status='waiting' found")

    return {
        "question": config.get("question", followup_msg.get("message", "")),
        "followup_type": config.get("followupType", "text"),
        "options": config.get("followupOptions", []),
        "data": config.get("followupData"),
        "agent_id": agent_id,
        "agent_name": config.get("agentName", ""),
        "message_id": message_id,
    }


def _extract_convo_id(response_data: dict) -> Optional[str]:
    """Extract conversation_id from a response dict."""
    if not response_data:
        return None
    return response_data.get("data", {}).get("conversation_id")


def _transform_conversation_response(conv):
    """
    Transform a ``ConversationWithMessages`` object (from ``chat_get``) into
    the same shape as a synchronous ``/v1/completions/chat`` response so that
    existing benchmark extraction logic works unchanged.
    """
    messages = conv.get("llm_conversation_messages", [])
    if not messages:
        return {
            "data": {
                "response": [],
                "agent_step_response": [],
                "conversation_id": str(conv.get("id", "")),
            }
        }

    # Use the last non-followup message for response extraction.
    # When status is WAITING, the last message is a "followup" message
    # with no response/agents — the actual data is on the generation message.
    last_message = messages[-1]
    for msg in reversed(messages):
        if msg.get("message_type") != "followup":
            last_message = msg
            break
    response_text = last_message.get("response") or ""

    # Build agent_step_response from agents
    agent_step_response = []
    for agent in last_message.get("llm_conversation_agents") or []:
        step_resp = agent.get("agent_step_response")
        if step_resp:
            try:
                parsed = (
                    json.loads(step_resp) if isinstance(step_resp, str) else step_resp
                )
            except (json.JSONDecodeError, TypeError):
                continue
            if isinstance(parsed, list):
                agent_step_response.extend(parsed)
            else:
                agent_step_response.append(parsed)

    return {
        "data": {
            "response": (
                [response_text] if isinstance(response_text, str) else response_text
            ),
            "agent_step_response": agent_step_response,
            "conversation_id": str(conv.get("id", "")),
            "session_id": conv.get("session_id", ""),
            "status": conv.get("status", ""),
        }
    }


def extract_tool_command(response_data, tool_names):
    """
    Extract the first matching tool call's arguments from a chat response.

    ``tool_names`` is a list of tool names to look for (e.g.
    ``["aws_execute"]``).  Returns the command string or ``None``.
    """
    if not response_data:
        return None
    data = response_data.get("data", {})
    steps = data.get("agent_step_response", [])
    for step in steps:
        call = step.get("Call", {}).get("tool_call", {}).get("function", {})
        if call.get("name") in tool_names:
            command = call.get("arguments", "")
            if command:
                return command
    return None


def extract_response_text(response_data):
    """Return the full text response from a chat response dict."""
    if not response_data:
        return ""
    data = response_data.get("data", {})
    resp = data.get("response", "")
    if isinstance(resp, list):
        return " ".join(map(str, resp))
    return resp or ""


def extract_conversation_id(response_data):
    """Return the conversation_id from a chat response dict."""
    if not response_data:
        return None
    return response_data.get("data", {}).get("conversation_id")
