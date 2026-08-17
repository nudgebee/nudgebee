"""Live "Thinking Steps" panel for NuBi Slack conversations.

While llm-server works on a question, its planner persists every tool call to
Postgres — the same rows the web UI's live view polls through api-server. A
per-question daemon thread polls that delta feed (`ai_get_conversation_v3`)
and mirrors it into a native Slack streaming message in the thread, so a long
investigation shows what it is doing instead of silence.

Everything here is best-effort by design: any failure means no panel (or a
frozen one), never a failed conversation. All Slack and HTTP calls run on the
poller's own daemon thread — nothing new lands on the shared event loop.
"""

import logging
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone

import requests
from slack_sdk.errors import SlackApiError

from notifications_server.configs import settings
from notifications_server.services.cache import Cache

LOG = logging.getLogger(__name__)

# Turn-terminal conversation statuses: the run stopped producing steps, either
# for good or until the user answers a follow-up.
_TERMINAL_CONVERSATION_STATUSES = {
    "COMPLETED",
    "FAILED",
    "KILLED",
    "TERMINATED",
    "WAITING",
    "WAITING_FOR_CLIENT_TOOL",
}

_TOOL_STATUS_TO_TASK_STATUS = {
    "IN_PROGRESS": "in_progress",
    "WAITING": "in_progress",
    "WAITING_FOR_CLIENT": "in_progress",
    "SUCCESS": "complete",
    "EMPTY_RESULT": "complete",
    "ERROR": "error",
    "FAILURE": "error",
    "TERMINATED": "error",
}
_SETTLED_TASK_STATUSES = ("complete", "error")

# Slack caps task_update fields at 256 chars.
_TASK_FIELD_LIMIT = 250
# Header shown from panel creation until the first tool title arrives.
_INITIAL_HEADER = "Thinking"
# Placeholder stream key written before chat.startStream returns a real ts, so
# a settle racing the stream's creation always finds a key to clear. Each claim
# carries a unique suffix so overlapping turns can't mistake each other's claim
# for their own.
_STREAM_PENDING_PREFIX = "pending"
_MAX_POLL_FAILURES = 3
# Initial `since` looks slightly behind the question time so rows written
# between the LLM 202 and the first poll aren't missed by clock skew.
_INITIAL_SINCE_SKEW_SECONDS = 5

_active_pollers = 0
_pollers_lock = threading.Lock()


def start_progress_poller(common_service, cached_entry, thread_ts, session_id):
    """Spawn the panel poller for one Slack question. Never raises.

    ``session_id`` must be the one actually sent to llm-server for this turn
    (the cached entry's copy can differ on the follow-up path).
    """
    global _active_pollers
    try:
        entry = dict(cached_entry or {})
        entry["session_id"] = session_id
        required = ("team_id", "channel_id", "slack_user_id", "session_id", "account_id", "tenant_id", "user_id")
        missing = [key for key in required if not entry.get(key)]
        if missing:
            LOG.debug("thinking steps: skipped, cached entry missing %s", missing)
            return
        with _pollers_lock:
            if _active_pollers >= settings.slack.thinking_steps_max_pollers:
                LOG.warning("thinking steps: poller cap reached, no panel for %s", thread_ts)
                return
            _active_pollers += 1
        try:
            threading.Thread(
                target=_run_poller,
                args=(common_service, entry, thread_ts),
                daemon=True,
                name=f"slack-progress-{thread_ts}",
            ).start()
        except Exception:
            with _pollers_lock:
                _active_pollers -= 1
            raise
    except Exception as e:
        LOG.warning("thinking steps: failed to start poller: %s", e)


def stop_progress_stream(common_service, cached_entry, channel_id, team_id, thread_ts):
    """Settle the panel (called by the final/follow-up/error handlers). Never raises.

    Clears the stream key first so a live poller sees the mismatch and exits
    without double-stopping.
    """
    try:
        cache = Cache()
        entry = cache.get_event_entry(thread_ts) or cached_entry or {}
        stream_ts = entry.get("stream_ts")
        if not stream_ts:
            return
        cache.remove_event_keys(thread_ts, ["stream_ts"])
        if not _is_pending(stream_ts):
            _stop_stream(common_service, team_id, channel_id, stream_ts)
        # A pending stream has no ts to stop yet; clearing the key is enough —
        # the poller notices the mismatch after startStream and stops its own.
    except Exception as e:
        LOG.debug("thinking steps: settle stop failed: %s", e)


def _run_poller(common_service, entry, thread_ts):
    global _active_pollers
    try:
        _poll(common_service, entry, thread_ts)
    except Exception as e:
        LOG.warning("thinking steps: poller for %s died: %s", thread_ts, e)
    finally:
        with _pollers_lock:
            _active_pollers -= 1


def _poll(common_service, entry, thread_ts):
    cache = Cache()
    bot = common_service.get_slack_installation(entry["team_id"])
    if not bot:
        return
    token = bot.token

    stream_ts = _open_stream(common_service, cache, entry, thread_ts, token)
    if not stream_ts:
        return
    try:
        _stream_updates(common_service, cache, entry, thread_ts, token, stream_ts)
    finally:
        current = cache.get_event_entry(thread_ts)
        if current and current.get("stream_ts") == stream_ts:
            cache.remove_event_keys(thread_ts, ["stream_ts"])
            _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)


def _open_stream(common_service, cache, entry, thread_ts, token):
    """Start the streaming message and record ownership; None means no panel."""
    # A previous turn's stream can still be open here (its poller died or was
    # superseded mid-run). Close it so the thread never shows two live panels.
    previous = (cache.get_event_entry(thread_ts) or {}).get("stream_ts")
    if previous:
        cache.remove_event_keys(thread_ts, ["stream_ts"])
        if not _is_pending(previous):
            _stop_stream(common_service, entry["team_id"], entry["channel_id"], previous)

    # Claim the panel before the stream exists, so a settle racing startStream
    # finds a key to clear instead of no-opping. The claim is unique per turn
    # so an overlapping turn's claim never passes for ours.
    claim = f"{_STREAM_PENDING_PREFIX}-{uuid.uuid4().hex}"
    if not cache.update_event_entry(thread_ts, stream_ts=claim):
        return None

    try:
        response = common_service.slack_app.client.start_stream(
            token=token,
            channel_id=entry["channel_id"],
            thread_ts=thread_ts,
            recipient_team_id=entry["team_id"],
            recipient_user_id=entry["slack_user_id"],
            task_display_mode="timeline",
            chunks=[{"type": "plan_update", "title": _INITIAL_HEADER}],
        )
        stream_ts = response["ts"]
    except SlackApiError as e:
        LOG.info("thinking steps: start_stream refused (%s), no panel for %s", _slack_error(e), thread_ts)
        current = cache.get_event_entry(thread_ts)
        if current and current.get("stream_ts") == claim:
            cache.remove_event_keys(thread_ts, ["stream_ts"])
        return None

    # If our claim is gone (settle raced us, entry expired, or a newer turn
    # took over), this stream has no owner — close it immediately.
    current = cache.get_event_entry(thread_ts)
    if not current or current.get("stream_ts") != claim:
        _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)
        return None
    if not cache.update_event_entry(thread_ts, stream_ts=stream_ts):
        _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)
        return None
    return stream_ts


def _stream_updates(common_service, cache, entry, thread_ts, token, stream_ts):
    sent_statuses = {}
    titles = {}
    last_header = _INITIAL_HEADER
    failures = 0
    since = (datetime.now(timezone.utc) - timedelta(seconds=_INITIAL_SINCE_SKEW_SECONDS)).isoformat()
    deadline = time.monotonic() + settings.slack.thinking_steps_max_minutes * 60

    # Fetch-first (sleep at the bottom): the panel opens before the LLM request
    # is even sent, so the first delta should land as soon as rows exist.
    while True:
        current = cache.get_event_entry(thread_ts)
        if current is None:
            # Entry expired mid-run: nothing can ever settle the panel, so
            # close it now (the caller's ownership check needs the entry).
            _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)
            return
        if current.get("stream_ts") != stream_ts:
            # The settle handler cleaned up, or a newer turn owns the panel.
            return

        if time.monotonic() > deadline:
            return

        delta = _fetch_delta(entry, since)
        if delta is None:
            failures += 1
            if failures >= _MAX_POLL_FAILURES:
                return
            time.sleep(settings.slack.thinking_steps_poll_seconds)
            continue
        failures = 0
        since = delta.get("cursor") or since

        chunks = _build_chunks(delta.get("tool_calls"), sent_statuses, titles)
        header, last_header = _header_chunk(sent_statuses, titles, last_header)
        if header:
            chunks.append(header)
        if chunks:
            try:
                common_service.slack_app.client.append_stream(
                    token=token, channel_id=entry["channel_id"], ts=stream_ts, chunks=chunks
                )
            except SlackApiError as e:
                LOG.info("thinking steps: append refused (%s), dropping panel for %s", _slack_error(e), thread_ts)
                return

        conversation = delta.get("conversation") or {}
        if (conversation.get("status") or "").upper() in _TERMINAL_CONVERSATION_STATUSES:
            # Give the settle handler a moment to close the panel itself so
            # the stop lands in answer order; fall through if it never came.
            time.sleep(2)
            return

        time.sleep(settings.slack.thinking_steps_poll_seconds)


def _fetch_delta(entry, since):
    try:
        response = requests.post(
            settings.services.api_server + "/rpc/ai",
            json={
                "action": {"name": "ai_get_conversation_v3"},
                "input": {
                    "request": {
                        "account_id": entry["account_id"],
                        "session_id": entry["session_id"],
                        "since": since,
                    }
                },
                "session_variables": {
                    "tenant_id": entry["tenant_id"],
                    "user_id": entry["user_id"],
                },
            },
            headers={"X-ACTION-TOKEN": settings.action_api_server_token},
            timeout=10,
        )
        response.raise_for_status()
        return response.json()
    except (requests.RequestException, ValueError) as e:
        LOG.debug("thinking steps: delta fetch failed: %s", e)
        return None


def _build_chunks(tool_calls, sent_statuses, titles=None):
    chunks = []
    for row in sorted(tool_calls or [], key=lambda r: r.get("updated_at") or ""):
        row_id = row.get("id")
        if not row_id:
            continue
        status = _TOOL_STATUS_TO_TASK_STATUS.get((row.get("status") or "").upper(), "in_progress")
        if sent_statuses.get(row_id) == status or sent_statuses.get(row_id) in _SETTLED_TASK_STATUSES:
            continue
        title = _task_title(row.get("tool_name"))
        chunks.append(
            {
                "type": "task_update",
                "id": row_id,
                "title": title,
                "status": status,
            }
        )
        sent_statuses[row_id] = status
        if titles is not None:
            titles[row_id] = title
    return chunks


def _header_chunk(sent_statuses, titles, last_header):
    """The panel's collapsed header: the currently running tool titles, comma-
    joined. Sent as a plan_update only when the active set changes; between
    tools the last header is kept rather than flickering to empty."""
    active = [titles[i] for i, s in sent_statuses.items() if s == "in_progress" and i in titles]
    if not active:
        return None, last_header
    header = ", ".join(dict.fromkeys(active))[:_TASK_FIELD_LIMIT]
    if header == last_header:
        return None, last_header
    return {"type": "plan_update", "title": header}, header


def _is_pending(stream_ts):
    return isinstance(stream_ts, str) and stream_ts.startswith(_STREAM_PENDING_PREFIX)


def _task_title(tool_name):
    title = (tool_name or "Working").replace("_", " ").strip() or "Working"
    return (title[:1].upper() + title[1:])[:_TASK_FIELD_LIMIT]


def _stop_stream(common_service, team_id, channel_id, stream_ts):
    try:
        bot = common_service.get_slack_installation(team_id)
        if not bot:
            return
        common_service.slack_app.client.stop_stream(token=bot.token, channel_id=channel_id, ts=stream_ts)
    except Exception as e:
        LOG.debug("thinking steps: stop_stream failed (likely already stopped): %s", e)


def _slack_error(e):
    try:
        return e.response.data.get("error")
    except Exception:
        return str(e)
