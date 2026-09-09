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

import json
import logging
import re
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone

import requests
from slack_sdk.errors import SlackApiError

from notifications_server.configs import settings
from notifications_server.services.cache import Cache

LOG = logging.getLogger(__name__)

# Turn-terminal conversation statuses: the run stopped producing steps for
# good. Mirrors llm-server's own IsTerminalConversationStatus (agents/core/
# interface.go) — WAITING and WAITING_FOR_CLIENT_TOOL are deliberately NOT
# here: llm-server treats both as resumable in-flight state (a delegate/
# sub-agent step can leave the parent conversation reading WAITING_FOR_
# CLIENT_TOOL, or even WAITING, without a real end-user follow-up pending),
# so this poller closing the panel on either would end it mid-investigation.
# The genuine "Nubi asked a follow-up question" case already gets its own
# explicit stop_progress_stream call from handle_followup_response.
_TERMINAL_CONVERSATION_STATUSES = {
    "COMPLETED",
    "FAILED",
    "KILLED",
    "TERMINATED",
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
# Task titles favor the LLM's own thought over the bare tool name (reverting
# part of b9bb0ddb54's "bare tool titles" simplification) so a step reads as
# what it's actually doing, and so two rows sharing a tool_name (e.g. the
# same recommendation executed twice) don't render as identical titles.
# Hard-capped since thought is free LLM prose with no length contract.
_TASK_TITLE_CHAR_LIMIT = 80
# Header shown from panel creation until the first tool title arrives.
_INITIAL_HEADER = "Thinking"
# Synthetic first task so the panel opens already populated (Slack renders a
# bare plan_update as plain "Thinking..." text; adding a task turns it into
# the collapsible panel right away). Doubles as the panel's "something is
# always active" placeholder: real tool tasks can all settle with the next
# one not started yet, and with nothing in_progress the panel would read as
# finished mid-turn. Whenever no real tool is in_progress, this task is
# (re)opened — titled _CONTINUING_TASK_TITLE once real activity has started
# — until either a real tool takes over or the final flush closes the panel.
_INITIAL_TASK_ID = "starting"
_INITIAL_TASK_TITLE = "Understanding your query"
_CONTINUING_TASK_TITLE = "investigating..."
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
        cache.remove_event_keys(thread_ts, ["stream_ts", "progress_since"])
        if not _is_pending(stream_ts):
            # This settle can race the poller's own poll cadence: a tool call
            # that completed in the turn's final seconds may not have been
            # picked up yet. One last fetch from turn start catches it before
            # the stream closes.
            _flush_final_delta(common_service, entry, team_id, channel_id, stream_ts, entry.get("progress_since"))
            _stop_stream(common_service, team_id, channel_id, stream_ts)
        # A pending stream has no ts to stop yet; clearing the key is enough —
        # the poller notices the mismatch after startStream and stops its own.
    except Exception as e:
        LOG.debug("thinking steps: settle stop failed: %s", e)


_FLUSH_FETCH_TIMEOUT_SECONDS = 5


def _flush_final_delta(common_service, entry, team_id, channel_id, stream_ts, since):
    """Closes out the panel: always relabels the synthetic placeholder task
    back to _INITIAL_TASK_TITLE/complete, plus a best-effort catch-up fetch
    for any tool call that hasn't been sent yet.

    Uses the turn-start cursor (not the poller's own, thread-local advancing
    one) and a fresh dedupe map, so a successful catch-up re-sends every tool
    call's current status regardless of what was already sent — harmless
    (Slack task_update is an upsert by id) and guarantees nothing from this
    turn is missed, including when the poller closes the panel itself (a
    terminal status can still be read a beat before the turn's own final
    write, e.g. on a reused session where a stale prior-turn row briefly
    makes it look active).

    Called both from the poller's own finally block and, via
    stop_progress_stream, inline from the Slack response handlers on the
    shared event loop — the fetch uses a tight timeout so the latter case
    can't gate the user's answer on api-server latency. The catch-up fetch
    can therefore fail or time out under real load; when it does, the
    placeholder relabel must still go out on its own; skipping the whole
    flush left the panel stuck showing the mid-run "investigating..." title
    forever (seen live 2026-08-19). A missed catch-up tool call is a
    cosmetic loss, not a correctness one — but a missed placeholder relabel
    isn't.
    """
    try:
        bot = common_service.get_slack_installation(team_id)
        if not bot:
            return
        chunks = []
        if since:
            # Isolated from the guaranteed finalize below: even an outright
            # exception here (not just a None/timeout return) must not skip
            # relabeling the placeholder.
            try:
                delta = _fetch_delta(entry, since, _FLUSH_FETCH_TIMEOUT_SECONDS)
                if delta is not None:
                    chunks = _build_chunks(delta.get("tool_calls"), {}, force_settle=True)
            except Exception as e:
                LOG.debug("thinking steps: final catch-up fetch failed: %s", e)
        chunks.append(
            {"type": "task_update", "id": _INITIAL_TASK_ID, "title": _INITIAL_TASK_TITLE, "status": "complete"}
        )
        common_service.slack_app.client.append_stream(
            token=bot.token, channel_id=channel_id, ts=stream_ts, chunks=chunks
        )
    except Exception as e:
        LOG.debug("thinking steps: final catch-up flush failed: %s", e)


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
        LOG.info("thinking steps: no Slack installation for team %s, no panel for %s", entry["team_id"], thread_ts)
        return
    token = bot.token

    # Fixed for the whole turn (never advanced): the settle handler's final
    # catch-up flush re-fetches from this same point, not the poller's own
    # advancing cursor, so it re-covers the entire turn rather than just the
    # gap since the poller's last iteration.
    since = (datetime.now(timezone.utc) - timedelta(seconds=_INITIAL_SINCE_SKEW_SECONDS)).isoformat()
    stream_ts = _open_stream(common_service, cache, entry, thread_ts, token, since)
    if not stream_ts:
        return
    try:
        _stream_updates(common_service, cache, entry, thread_ts, token, stream_ts, since)
    finally:
        current = cache.get_event_entry(thread_ts)
        if current and current.get("stream_ts") == stream_ts:
            cache.remove_event_keys(thread_ts, ["stream_ts", "progress_since"])
            # Same rationale as the settle handler's flush: whatever made the
            # poller stop (a trusted terminal status, the deadline, giving up
            # on fetch failures) can still land a beat before the turn's own
            # last write, so take one more look before actually closing.
            _flush_final_delta(common_service, entry, entry["team_id"], entry["channel_id"], stream_ts, since)
            _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)


def _open_stream(common_service, cache, entry, thread_ts, token, since):
    """Start the streaming message and record ownership; None means no panel."""
    # A previous turn's stream can still be open here (its poller died or was
    # superseded mid-run). Close it so the thread never shows two live panels.
    previous = (cache.get_event_entry(thread_ts) or {}).get("stream_ts")
    if previous:
        cache.remove_event_keys(thread_ts, ["stream_ts", "progress_since"])
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
            # "plan" renders ONE panel (title + nested tasks); "timeline"
            # renders every task as its own card, which reads as multiple
            # loaders — confirmed on dev.
            task_display_mode="plan",
            chunks=[
                {"type": "plan_update", "title": _INITIAL_HEADER},
                {"type": "task_update", "id": _INITIAL_TASK_ID, "title": _INITIAL_TASK_TITLE, "status": "in_progress"},
            ],
        )
        stream_ts = response["ts"]
    except SlackApiError as e:
        LOG.info("thinking steps: start_stream refused (%s), no panel for %s", _slack_error(e), thread_ts)
        current = cache.get_event_entry(thread_ts)
        if current and current.get("stream_ts") == claim:
            cache.remove_event_keys(thread_ts, ["stream_ts", "progress_since"])
        return None

    # If our claim is gone (settle raced us, entry expired, or a newer turn
    # took over), this stream has no owner — close it immediately.
    current = cache.get_event_entry(thread_ts)
    if not current or current.get("stream_ts") != claim:
        _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)
        return None
    if not cache.update_event_entry(thread_ts, stream_ts=stream_ts, progress_since=since):
        _stop_stream(common_service, entry["team_id"], entry["channel_id"], stream_ts)
        return None
    return stream_ts


def _stream_updates(common_service, cache, entry, thread_ts, token, stream_ts, since):
    sent_statuses = {_INITIAL_TASK_ID: "in_progress"}
    # Set once, on the first reopen, and never reverted: once real activity
    # has happened the "Understanding your query" framing no longer fits, so
    # every later settle/reopen of this task reuses _CONTINUING_TASK_TITLE
    # instead of flapping the title back and forth.
    placeholder_title = _INITIAL_TASK_TITLE
    failures = 0
    # On a reused session (an existing Slack thread with prior completed
    # turns), `conversation.status` can still read COMPLETED from the last
    # turn for a moment after this one starts — llm-server's async endpoint
    # returns 202 as soon as the request is queued, before a worker actually
    # dequeues it and flips the row's status. A terminal status is only
    # trusted once this turn has shown up as real activity: a tool call, or a
    # message with a real response body. A bare new message row is NOT
    # enough on its own — llm-server inserts the human message row with an
    # empty response and writes its ack_message (~seconds in) via a separate
    # update that never touches response/status, well before the real worker
    # picks up the request. Trusting that bare row tripped this guard on the
    # ack and closed the panel before the investigation even started (seen
    # live 2026-08-19: panel froze on "Understanding your query" for a
    # 5-minute, 19-tool-call turn). A populated response is only ever written
    # once real generation has happened, which is what this guard needs.
    turn_activity_seen = False
    deadline = time.monotonic() + settings.slack.thinking_steps_max_minutes * 60

    # Fetch-first (sleep at the bottom): the panel opens before the LLM request
    # is even sent, so the first delta should land as soon as rows exist.
    while True:
        current = cache.get_event_entry(thread_ts)
        if current is None:
            # Entry expired mid-run: nothing can ever settle the panel, so
            # close it now (the caller's ownership check needs the entry).
            LOG.info("thinking steps: cache entry expired mid-run for %s, closing panel", thread_ts)
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
                LOG.warning("thinking steps: giving up after %d consecutive fetch failures for %s", failures, thread_ts)
                return
            time.sleep(settings.slack.thinking_steps_poll_seconds)
            continue
        failures = 0
        since = delta.get("cursor") or since
        if delta.get("tool_calls") or any((m.get("response") or "").strip() for m in delta.get("messages") or []):
            turn_activity_seen = True

        chunks = _build_chunks(delta.get("tool_calls"), sent_statuses)
        # A real tool finishing doesn't guarantee the next one has started
        # yet (or that llm-server is done): with nothing else in the panel
        # currently in_progress, it would read as finished mid-turn. So
        # _INITIAL_TASK_ID tracks the gaps — settled while a real tool is
        # active, (re)opened the moment none are, right up to the final
        # flush (_flush_final_delta), which is what actually closes it out.
        any_tool_active = any(
            status == "in_progress" for tid, status in sent_statuses.items() if tid != _INITIAL_TASK_ID
        )
        placeholder_status = sent_statuses.get(_INITIAL_TASK_ID)
        if any_tool_active and placeholder_status == "in_progress":
            chunks.append(
                {"type": "task_update", "id": _INITIAL_TASK_ID, "title": placeholder_title, "status": "complete"}
            )
            sent_statuses[_INITIAL_TASK_ID] = "complete"
        elif not any_tool_active and placeholder_status != "in_progress":
            placeholder_title = _CONTINUING_TASK_TITLE
            chunks.append(
                {"type": "task_update", "id": _INITIAL_TASK_ID, "title": placeholder_title, "status": "in_progress"}
            )
            sent_statuses[_INITIAL_TASK_ID] = "in_progress"
        if chunks:
            try:
                common_service.slack_app.client.append_stream(
                    token=token, channel_id=entry["channel_id"], ts=stream_ts, chunks=chunks
                )
            except SlackApiError as e:
                LOG.info("thinking steps: append refused (%s), dropping panel for %s", _slack_error(e), thread_ts)
                return

        conversation = delta.get("conversation") or {}
        status = (conversation.get("status") or "").upper()
        if status == "IN_PROGRESS":
            turn_activity_seen = True
        if status in _TERMINAL_CONVERSATION_STATUSES:
            if not turn_activity_seen:
                LOG.info(
                    "thinking steps: ignoring stale terminal status %s for %s (no turn activity observed yet)",
                    status,
                    thread_ts,
                )
            else:
                # Give the settle handler a moment to close the panel itself
                # so the stop lands in answer order; fall through if it never
                # came.
                time.sleep(2)
                return

        time.sleep(settings.slack.thinking_steps_poll_seconds)


def _fetch_delta(entry, since, timeout=10):
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
            timeout=timeout,
        )
        response.raise_for_status()
        return response.json()
    except (requests.RequestException, ValueError) as e:
        LOG.debug("thinking steps: delta fetch failed: %s", e)
        return None


def _build_chunks(tool_calls, sent_statuses, force_settle=False):
    """``force_settle`` mirrors what the final catch-up flush is about to do to
    each chunk's status (see _flush_final_delta) so the title truncation
    decision below - full text while in_progress, capped once settled -
    matches the status the task will actually be left showing, instead of the
    still-in_progress status this row happens to have in the DB right now."""
    chunks = []
    for row in sorted(tool_calls or [], key=lambda r: r.get("updated_at") or ""):
        row_id = row.get("id")
        if not row_id:
            continue
        status = _TOOL_STATUS_TO_TASK_STATUS.get((row.get("status") or "").upper(), "in_progress")
        if force_settle and status not in _SETTLED_TASK_STATUSES:
            status = "complete"
        if sent_statuses.get(row_id) == status or sent_statuses.get(row_id) in _SETTLED_TASK_STATUSES:
            continue
        chunks.append(
            {
                "type": "task_update",
                "id": row_id,
                "title": _task_title(row.get("tool_name"), row.get("thought"), status, row.get("parameters")),
                "status": status,
            }
        )
        sent_statuses[row_id] = status
    return chunks


def _is_pending(stream_ts):
    return isinstance(stream_ts, str) and stream_ts.startswith(_STREAM_PENDING_PREFIX)


# Verbs whose gerund doubles the final consonant (run -> running, not runing).
# Not exhaustive English grammar - just the short common verbs likely to show
# up in an SRE/DevOps thought, since a title is cosmetic and a rare miss here
# is harmless.
_GERUND_DOUBLES_CONSONANT = {"run", "scan", "stop", "get", "set", "put", "plan", "map", "tag", "log", "drop"}


def _gerund(verb):
    verb = verb.lower()
    if verb in _GERUND_DOUBLES_CONSONANT:
        return verb + verb[-1] + "ing"
    if verb.endswith("ie"):
        return verb[:-2] + "ying"
    if verb.endswith("e") and not verb.endswith("ee"):
        return verb[:-1] + "ing"
    return verb + "ing"


# LLM thoughts default to narrating intent ("I will...", "Let's...", "I need
# to...") rather than stating the action, which reads fine as an internal
# monologue but wastes the little width a Slack task title has. Rewritten to
# lead with the action itself instead, mirroring the same convention applied
# to this assistant's own status text.
_DELIBERATION_VERB_LEAD_IN = re.compile(
    r"^(?:let[’']s|let us|let me|i will|i[’']ll|i need to|i[’']m going to)\s+(\w+)\s*", re.IGNORECASE
)
# These have nothing to convert - the real verb is already the next word
# ("I have identified"/"I've identified", "I attempted") - just drop the
# subject in front of it. The bare "I <verb>" form (no have/'ve) is only
# safe to drop when <verb> is past tense ("I attempted" -> "Attempted..."):
# present-tense/modal openers ("I am", "I can", "I should") read as broken
# English with the subject removed, so those are left alone.
_DELIBERATION_STRIP_LEAD_INS = (
    re.compile(r"^i(?:\s+have|[’']ve)\s+", re.IGNORECASE),
    re.compile(r"^i\s+(?=\w+ed\b)", re.IGNORECASE),
)


def _drop_deliberation_prefix(thought):
    match = _DELIBERATION_VERB_LEAD_IN.match(thought)
    if match:
        rewritten = _gerund(match.group(1)) + " " + thought[match.end() :]
        return rewritten.rstrip()
    for pattern in _DELIBERATION_STRIP_LEAD_INS:
        match = pattern.match(thought)
        if match:
            return thought[match.end() :]
    return thought


_AND_WORD = re.compile(r"\band\b", re.IGNORECASE)
# Backticks/asterisks/underscores wrapping a word are markdown syntax
# ("`code`", "*bold*", "_word_", "__bold__") and read as noise in a title
# (no markdown support in a Slack task title). But an underscore between two
# alphanumerics is the word separator in a snake_case identifier
# ("cloud_command") - dropping it would collapse it into an unreadable
# run-on - so only strip these characters where they aren't sandwiched
# between alphanumeric characters, i.e. where they're wrapping rather than
# embedded.
_MARKDOWN_WRAPPER_CHARS = re.compile(r"(?<![A-Za-z0-9])[`*_]+|[`*_]+(?![A-Za-z0-9])")

# `_SELF_DESCRIBING` values already name their own action ("kubectl ...",
# "SELECT ...") and go in verbatim; `_TARGET` values are a bare argument (rg
# pattern, glob) that only reads as a command once led with the tool name.
_PARAM_SELF_DESCRIBING_KEYS = ("command", "query", "sql")
_PARAM_TARGET_KEYS = ("pattern", "path_glob")


def _lead_with_tool(tool_name, text):
    tool_name = (tool_name or "").strip()
    return f"{tool_name} {text}" if tool_name else text


def _command_from_parameters(parameters, tool_name=None):
    """One-liner describing what a tool call runs, from its persisted
    `parameters`. Fallback for rows the planner left with no `thought` (relay
    sub-steps, code-analysis file ops) that otherwise render as an
    undifferentiated stack of one bare tool name. "" when nothing readable.

    Only an allow-list of command-ish keys is surfaced, so secret keys
    (`github_token`, `credentials`) can never reach the panel.

    `parameters` is a JSON string in every row seen so far, but the field is
    loosely typed upstream - accept a pre-parsed dict/list too rather than
    crash the poller on an unexpected shape.
    """
    if not parameters:
        return ""
    if isinstance(parameters, (dict, list)):
        obj = parameters
    else:
        raw = str(parameters).strip()
        if not raw:
            return ""
        if raw[0] not in "{[":
            return " ".join(raw.split())
        try:
            obj = json.loads(raw)
        except ValueError:
            return ""
    if isinstance(obj, list):
        return _lead_with_tool(tool_name, " ".join(str(x) for x in obj))
    if not isinstance(obj, dict):
        return ""
    for key in _PARAM_SELF_DESCRIBING_KEYS:
        val = obj.get(key)
        if isinstance(val, str) and val.strip():
            return " ".join(val.split())
    for key in _PARAM_TARGET_KEYS:
        val = obj.get(key)
        if isinstance(val, str) and val.strip():
            return _lead_with_tool(tool_name, " ".join(val.split()))
    args = obj.get("args")
    if isinstance(args, list) and args:
        return _lead_with_tool(tool_name, " ".join(str(x) for x in args))
    path = obj.get("file_path") or obj.get("path")
    if isinstance(path, str) and path.strip():
        start, end = obj.get("start_line"), obj.get("end_line")
        loc = f"{path}:{start}-{end}" if isinstance(start, int) and isinstance(end, int) else path
        return _lead_with_tool(tool_name, loc)
    return ""


def _task_title(tool_name, thought=None, status=None, parameters=None):
    thought = " ".join((thought or "").split())
    is_command = False
    if thought:
        title = _drop_deliberation_prefix(thought) or thought
        title = _AND_WORD.sub("&", title)
        title = _MARKDOWN_WRAPPER_CHARS.sub("", title) or title
        title = title[:1].upper() + title[1:]
    else:
        command = _command_from_parameters(parameters, tool_name)
        if command:
            # Verbatim - no capitalisation, no and/markdown substitutions: those
            # would mangle real syntax (jsonpath `{range .items[*]}`).
            title = command
            is_command = True
        else:
            title = (tool_name or "Working").replace("_", " ").strip() or "Working"
            title = _AND_WORD.sub("&", title)
            title = _MARKDOWN_WRAPPER_CHARS.sub("", title) or title
            title = title[:1].upper() + title[1:]
    # An in-progress thought is left uncapped (full text for the row the user is
    # watching); an in-progress command is still capped - a 200-char
    # kubectl/jsonpath line is noise, and a cropped command still reads.
    if (status != "in_progress" or is_command) and len(title) > _TASK_TITLE_CHAR_LIMIT:
        # Back up to the last full word rather than cutting mid-word ("clu…")
        # - drop the partial word entirely instead of showing a fragment of
        # it. Falls back to the raw cut only when there's no word boundary to
        # use (one long unbroken token).
        truncated = title[: _TASK_TITLE_CHAR_LIMIT - 1]
        last_space = truncated.rfind(" ")
        if last_space > 0:
            truncated = truncated[:last_space]
        title = truncated.rstrip() + "…"
    return title[:_TASK_FIELD_LIMIT]


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
