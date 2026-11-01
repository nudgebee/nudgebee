"""The "Thinking…" message of a Teams chat question.

Posted when a question goes to Nubi, updated with Nubi's steps by a poller
thread, and replaced by the answer (or follow-up question, or error) when it
arrives, possibly on another pod. Ownership is a per-turn claim in the chat's
cached entry:

- a new question writes a new thinking_id and thinking_claim;
- the handler that replaces the card takes both out of the entry first, and
  only while the claim is still the one it read;
- every update of the message, poller or handler, holds a Redis lock on the
  message id, and the poller re-checks its claim under that lock.

So the poller can never write steps over an answer, and an answer is never
dropped: when the card can't be replaced, the caller sends a new message.
"""

import asyncio
import logging
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone
from typing import Optional

from botbuilder.schema import Activity

from notifications_server.configs.settings import settings
from notifications_server.services.cache import Cache
from notifications_server.services.ms_teams.bot_sender import TeamsBotError, card_activity, get_sender
from notifications_server.services.slack_progress import _TOOL_STATUS_TO_TASK_STATUS, _fetch_delta, _task_title

LOG = logging.getLogger(__name__)

# Above the sender's worst case for one update (3 attempts, retry waits capped at 10s).
LOCK_SECONDS = 30
LOCK_WAIT_STEP_SECONDS = 0.25
MAX_UPDATES = 30
MAX_STEPS_SHOWN = 8
MAX_POLL_FAILURES = 3
# Looks slightly behind the question time so steps written before the first poll aren't missed.
SINCE_SKEW_SECONDS = 5

_STEP_MARKS = {"complete": "✓", "error": "✗"}

_active_pollers = 0
_pollers_lock = threading.Lock()


def thinking_card(steps) -> dict:
    """The Thinking card for steps, a list of (title, status)."""
    body = [{"type": "TextBlock", "text": "Thinking…", "weight": "Bolder"}]
    shown = steps[-MAX_STEPS_SHOWN:]
    if len(steps) > len(shown):
        body.append(
            {"type": "TextBlock", "text": f"…{len(steps) - len(shown)} earlier steps", "isSubtle": True, "wrap": True}
        )
    if not shown:
        body.append({"type": "TextBlock", "text": "Understanding your question", "isSubtle": True, "wrap": True})
    for title, status in shown:
        body.append(
            {
                "type": "TextBlock",
                "text": f"{_STEP_MARKS.get(status, '…')} {title}",
                "wrap": True,
                "spacing": "Small",
                "isSubtle": status in _STEP_MARKS,
            }
        )
    return {
        "type": "AdaptiveCard",
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "version": "1.4",
        "body": body,
    }


async def begin(thread_ts, conversation_ref) -> bool:
    """Post the Thinking card for a new question and make this turn its owner.
    False when it's off or couldn't be posted; the caller replies as before."""
    if not settings.ms_teams.thinking_steps_enabled or not conversation_ref:
        return False
    cache = Cache()
    current = cache.get_event_entry(thread_ts) or {}
    if current.get("thinking_claim") and time.time() - current.get("thinking_at", 0) < _stale_after_seconds():
        # An earlier question still owns its card and will replace it with its
        # answer; this one replies as before.
        return False
    try:
        activity_id = await asyncio.to_thread(
            get_sender().send,
            conversation_ref.get("service_url"),
            conversation_ref.get("conversation_id"),
            card_activity(thinking_card([])),
        )
    except TeamsBotError as e:
        LOG.warning("Posting the Teams Thinking card in %s failed: %s", thread_ts, e)
        return False
    if not activity_id:
        return False
    if not cache.update_event_entry(
        thread_ts, thinking_id=activity_id, thinking_claim=uuid.uuid4().hex, thinking_at=time.time()
    ):
        LOG.warning("Couldn't record the Teams Thinking card %s for %s", activity_id, thread_ts)
        return False
    return True


def _stale_after_seconds() -> float:
    """A card older than the poller's limit belongs to a question whose answer
    never came, so a new question may take over."""
    return settings.slack.thinking_steps_max_minutes * 60


def start_poller(thread_ts) -> None:
    """Start updating this turn's Thinking card with Nubi's steps. Never raises."""
    global _active_pollers
    try:
        entry = Cache().get_event_entry(thread_ts) or {}
        required = ("thinking_id", "thinking_claim", "account_id", "session_id", "tenant_id", "user_id")
        if any(not entry.get(key) for key in required) or not entry.get("conversation_reference"):
            return
        with _pollers_lock:
            if _active_pollers >= settings.slack.thinking_steps_max_pollers:
                LOG.warning("Teams Thinking card: poller cap reached, no steps for %s", thread_ts)
                return
            _active_pollers += 1
        try:
            threading.Thread(target=_run_poller, args=(entry, thread_ts), daemon=True, name="teams-thinking").start()
        except Exception:
            with _pollers_lock:
                _active_pollers -= 1
            raise
    except Exception as e:
        LOG.warning("Teams Thinking card: failed to start poller for %s: %s", thread_ts, e)


async def replace(thread_ts, conversation_ref, activity: Activity) -> Optional[str]:
    """Put activity in place of this turn's Thinking card. The message's id, or
    None when there's no card to replace or Teams refused the update; the caller
    then sends it as a new message."""
    cache = Cache()
    claim = (cache.get_event_entry(thread_ts) or {}).get("thinking_claim")
    activity_id = cache.take_teams_thinking(thread_ts, claim)
    if not activity_id or not conversation_ref:
        return None
    token = uuid.uuid4().hex
    deadline = time.monotonic() + LOCK_SECONDS
    while not cache.lock_teams_message(activity_id, token, LOCK_SECONDS):
        if time.monotonic() > deadline:
            LOG.warning("Teams Thinking card %s stayed locked; sending the reply as a new message", activity_id)
            return None
        await asyncio.sleep(LOCK_WAIT_STEP_SECONDS)
    try:
        await asyncio.to_thread(
            get_sender().update,
            conversation_ref.get("service_url"),
            conversation_ref.get("conversation_id"),
            activity_id,
            activity,
        )
        return activity_id
    except TeamsBotError as e:
        LOG.warning("Replacing Teams Thinking card %s failed: %s", activity_id, e)
        return None
    finally:
        cache.unlock_teams_message(activity_id, token)


def _run_poller(entry, thread_ts) -> None:
    global _active_pollers
    try:
        _poll(entry, thread_ts)
    except Exception as e:
        LOG.warning("Teams Thinking card: poller for %s died: %s", thread_ts, e)
    finally:
        with _pollers_lock:
            _active_pollers -= 1


def _owns(cache, thread_ts, claim) -> bool:
    current = cache.get_event_entry(thread_ts)
    return bool(current) and current.get("thinking_claim") == claim


def _poll(entry, thread_ts) -> None:
    cache = Cache()
    claim, activity_id = entry["thinking_claim"], entry["thinking_id"]
    since = (datetime.now(timezone.utc) - timedelta(seconds=SINCE_SKEW_SECONDS)).isoformat()
    steps = {}  # tool call id -> (title, status), in the order they started
    shown = thinking_card([])
    updates = failures = 0
    deadline = time.monotonic() + settings.slack.thinking_steps_max_minutes * 60
    while time.monotonic() < deadline and updates < MAX_UPDATES and _owns(cache, thread_ts, claim):
        delta = _fetch_delta(entry, since)
        if delta is None:
            failures += 1
            if failures >= MAX_POLL_FAILURES:
                return
            time.sleep(settings.slack.thinking_steps_poll_seconds)
            continue
        failures = 0
        since = delta.get("cursor") or since
        for row in sorted(delta.get("tool_calls") or [], key=lambda r: r.get("updated_at") or ""):
            if row.get("id"):
                status = _TOOL_STATUS_TO_TASK_STATUS.get((row.get("status") or "").upper(), "in_progress")
                title = _task_title(row.get("tool_name"), row.get("thought"), status, row.get("parameters"))
                steps[row["id"]] = (title, status)
        card = thinking_card(list(steps.values()))
        if card != shown:
            updated = _update_owned(cache, entry, thread_ts, claim, activity_id, card)
            if updated is None:
                return
            if updated:
                shown = card
                updates += 1
        time.sleep(settings.slack.thinking_steps_poll_seconds)


def _update_owned(cache, entry, thread_ts, claim, activity_id, card):
    """Update the card while this turn still owns it. True when updated, False
    to try again next tick, None to stop: the turn no longer owns it, or Teams
    refused the update for a reason other than throttling."""
    token = uuid.uuid4().hex
    if not cache.lock_teams_message(activity_id, token, LOCK_SECONDS):
        return False
    try:
        if not _owns(cache, thread_ts, claim):
            return None
        ref = entry["conversation_reference"]
        get_sender().update(ref.get("service_url"), ref.get("conversation_id"), activity_id, card_activity(card))
        return True
    except TeamsBotError as e:
        if e.status == 429:
            LOG.warning("Teams throttled updates to Thinking card %s", activity_id)
            return False
        LOG.info("Updating Teams Thinking card %s failed, no more steps: %s", activity_id, e)
        return None
    finally:
        cache.unlock_teams_message(activity_id, token)
