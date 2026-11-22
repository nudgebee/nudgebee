"""Clicks in the remediation loop's Slack thread (#40734), decided by the server as the clicker.

The thread llm-server draws (remediation_thread.py) has the fix-options message (Use this fix, Raise a PR, I made
this change, Other fixes…), the approval card of a step Nubi wants to run (Approve, Deny), and a verdict message
(Resolve). Each click goes to the server path the Remediation tab uses, as the NudgeBee user Identity Sync links to the
clicker's Slack user id; this module decides nothing itself and checks no roles (custom-role grants make a role check
here wrong). An unlinked clicker is told to link their account. A refusal is told to the clicker alone (ephemeral),
and the thread is drawn again from the server's state, so a button seen as stale is gone next time anyone looks.

- Use this fix / Raise a PR: a structured pick (remediation_pick) into the alert's conversation, event-<main
  fingerprint>, with this thread as its reply_ref, so the approval it leads to comes back here. The call is made
  synchronously; the thread's cache entry (which routes the approval back) is written only once the pick is taken.
- I made this change: the execute endpoint's "manual" slot, a record (nothing runs).
- Other fixes…: a modal with the tab's reasons and a note, then a replan request.
- Approve / Deny: a modal with an optional note, then the conversation's existing follow-up answer with the note.
- Resolve: api-server's events_apply_remediation_resolve, as the clicker.

A modal is opened first thing in the handler: Slack's trigger_id lasts 3 seconds from the click."""

import json
import logging
import uuid
from typing import Optional, Tuple

import requests
from slack_sdk.errors import SlackApiError
from sqlalchemy.orm import Session

from notifications_server.configs.settings import settings
from notifications_server.services import remediation_thread as rt

LOG = logging.getLogger(__name__)

ACTION_PREFIX = "remediation_"
CALLBACK_REPLAN = "remediation_replan"
CALLBACK_ANSWER = "remediation_answer"
CALLBACKS = {CALLBACK_REPLAN, CALLBACK_ANSWER}

CHAT_PATH = "/v1/completions/chat"
PLAN_PATH = "/v1/analyze/remediation/plan"
SYNC_PATH = "/v1/analyze/remediation/thread/sync"
TRIAGE_PATH = "/rpc/triage"
RESOLVE_ACTION = "events_apply_remediation_resolve"
HTTP_TIMEOUT = 20

# The tab's replan reasons (app RemediationLoopPanel.jsx REPLAN_REASONS), in its order.
REPLAN_REASONS = [
    "Different cause",
    "Too risky",
    "Needs a change window",
    "We fix this another way",
    "Add a step",
    "Already done",
]
NOTE_LIMIT = 500

NOT_LINKED = (
    "Link your Slack account to {brand} first: ask a {brand} admin to link it in Identity Sync, then press the"
    " button again. Nothing was recorded."
)
UNREACHABLE = rt.UNREACHABLE
CANT_READ = rt.CANT_READ
TRIGGER_EXPIRED = "That took too long to open. Press the button again."
PICK_SENT = "Sent to {brand}. It prepares option {option} and asks for approval in this thread."
PR_SENT = "Sent to {brand}. It prepares a pull request for option {option} in this thread."
MANUAL_DONE = "Recorded: you made the change. {brand} checks whether the alert clears."
REPLAN_SENT = "Asked {brand} for other fixes. They replace these when they are ready."
RESOLVED = "Resolved. The event is closed in your name."
ANSWER_GONE = "This approval was already answered or is no longer waiting, so nothing was recorded."
RETIRED = (
    "These buttons were retired: with the remediation loop on, a step runs only after {brand} asks for approval."
    " The current fix options are posted in this thread: pick one there with *Use this fix*, or on the Remediation tab."
)


# --- tokens --------------------------------------------------------------------------------------------------


def _uuids(parts):
    return tuple(str(uuid.UUID(p)) for p in parts)


def parse_plan_token(token) -> Optional[Tuple[str, str, str, str]]:
    """(tenant, account, plan event, plan version) of a fix-options message, or None."""
    if not (isinstance(token, str) and token.startswith(rt.PLAN_TOKEN_PREFIX)):
        return None
    parts = token[len(rt.PLAN_TOKEN_PREFIX) :].split(":")
    if len(parts) != 4 or not parts[3]:
        return None
    try:
        return (*_uuids(parts[:3]), parts[3])
    except ValueError:
        return None


def parse_run_token(token) -> Optional[Tuple[str, str, str, str]]:
    """(tenant, account, plan event, run id) of a verdict message, or None."""
    if not (isinstance(token, str) and token.startswith(rt.RUN_TOKEN_PREFIX)):
        return None
    parts = token[len(rt.RUN_TOKEN_PREFIX) :].split(":")
    if len(parts) != 4:
        return None
    try:
        return _uuids(parts)
    except ValueError:
        return None


# --- routing -------------------------------------------------------------------------------------------------


def is_block_action(data) -> bool:
    actions = data.get("actions") if isinstance(data, dict) else None
    if not (isinstance(actions, list) and actions and isinstance(actions[0], dict)):
        return False
    return str(actions[0].get("action_id") or "").startswith(ACTION_PREFIX)


def is_view_submission(data) -> bool:
    return (
        isinstance(data, dict)
        and data.get("type") == "view_submission"
        and ((data.get("view") or {}).get("callback_id") in CALLBACKS)
    )


class Click:
    """What a click in the thread carries."""

    def __init__(self, data):
        action = data["actions"][0]
        self.action_id = action.get("action_id") or ""
        try:
            value = json.loads(action.get("value") or "{}")
        except ValueError:
            value = {}
        self.value = value if isinstance(value, dict) else {}
        self.kind = str(self.value.get("k") or "")
        self.channel = (data.get("channel") or {}).get("id")
        self.team = (data.get("team") or {}).get("id")
        self.user = (data.get("user") or {}).get("id")
        self.trigger_id = data.get("trigger_id")
        self.app_id = data.get("api_app_id")
        self.message = data.get("message") or {}
        self.message_ts = self.message.get("ts")
        self.thread_ts = self.message.get("thread_ts") or self.message_ts
        payload = (self.message.get("metadata") or {}).get("event_payload") or {}
        self.token = payload.get("token")
        self.main_fingerprint = payload.get("main_fingerprint") or ""


def handle_block_action(service, data) -> None:
    """A click on one of the thread's remediation buttons."""
    click = Click(data)
    service.common_service.app_id = click.app_id
    if click.kind == "link" or click.action_id.startswith(ACTION_PREFIX + "link_"):
        return  # a link button: Slack opens it
    if click.kind == "other":
        return _open_replan(service, click)
    if click.kind == "answer":
        return _open_answer(service, click)
    handler = {"pick": _pick, "pr": _pick, "manual": _manual, "resolve": _resolve}.get(click.kind)
    if handler is None:
        LOG.warning("remediation click with an unknown kind %r", click.kind)
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)
    return handler(service, click)


# --- helpers -------------------------------------------------------------------------------------------------


def _brand() -> str:
    return settings.urls.branding_name


def _tell(service, channel, team, user, thread_ts, text) -> None:
    """Tell the clicker alone."""
    try:
        service.common_service.post_slack_ephemeral_response(channel, team, user, text, thread_ts)
    except Exception:
        LOG.warning("remediation click: the clicker could not be told", exc_info=True)


def _linked(service, tenant_id, slack_user) -> Optional[str]:
    try:
        with Session(service.engine) as session:
            return rt.linked_user(session, tenant_id, slack_user)
    except Exception:
        LOG.warning("remediation click: the clicker's link was not read", exc_info=True)
        return None


def _llm_headers(tenant_id, user_id):
    from notifications_server.services.events import Events  # events imports message, which imports this

    return Events._with_llm_auth({"x-user-id": user_id, "x-tenant-id": tenant_id})


def _error_message(response) -> str:
    """The first error sentence of an llm-server or api-server answer, without the "remediation: " prefix."""
    try:
        body = response.json()
    except ValueError:
        return ""
    if not isinstance(body, dict):
        return ""
    errors = body.get("errors")
    if isinstance(errors, list) and errors:
        first = errors[0]
        message = first.get("message", "") if isinstance(first, dict) else str(first)
    else:
        message = body.get("message") or body.get("error") or ""
        if isinstance(message, dict):
            message = message.get("message") or ""
    return str(message or "").removeprefix("remediation: ").strip()


def resync(tenant_id, account_id, event_id, user_id) -> None:
    """Ask llm-server to draw the alert's thread again from its state. Best effort."""
    try:
        requests.post(
            settings.services.llm_server + SYNC_PATH,
            headers=_llm_headers(tenant_id, user_id),
            json={"input": {"request": {"account_id": account_id, "event_id": event_id}}},
            timeout=10,
        )
    except requests.RequestException as e:
        LOG.info("remediation click: thread not synced: %s", e)


def _refused(service, click, ids, user_id, response) -> None:
    """A refusal: the clicker is told why, and the thread is drawn again."""
    message = _error_message(response)
    if response.status_code == 403:
        text = f"You can't do that: {message or 'it needs write access on this account'}."
    else:
        text = f"Not done: {message or 'the request was refused'}"
    _tell(service, click.channel, click.team, click.user, click.thread_ts, text)
    tenant_id, account_id, event_id = ids
    resync(tenant_id, account_id, event_id, user_id)


# --- Use this fix / Raise a PR -------------------------------------------------------------------------------


def _pick(service, click: Click) -> None:
    parsed = parse_plan_token(click.token)
    option = click.value.get("o")
    if not parsed or not isinstance(option, int) or option < 1:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)
    tenant_id, account_id, event_id, plan_version = parsed
    user_id = _linked(service, tenant_id, click.user)
    if not user_id:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, NOT_LINKED.format(brand=_brand()))
    kind = "pr" if click.kind == "pr" else "run"
    session_id = "event-" + (click.main_fingerprint or event_id)
    reply_ref = f"{click.channel}-{click.thread_ts}"
    payload = {
        # The server writes the message itself from the pick (the same words as the tab's).
        "query": f"Go with option {option}.",
        "account_id": account_id,
        "user_id": user_id,
        "session_id": session_id,
        "source": "InstantNotification",
        "async": True,
        "reply_ref": reply_ref,
        "remediation_pick": {"event_id": event_id, "plan_version": plan_version, "option": option, "kind": kind},
    }
    try:
        response = requests.post(
            settings.services.llm_server + CHAT_PATH,
            headers=_llm_headers(tenant_id, user_id),
            json=payload,
            timeout=HTTP_TIMEOUT,
        )
    except requests.RequestException as e:
        LOG.warning("remediation pick: llm-server unreachable: %s", e)
        return _tell(
            service, click.channel, click.team, click.user, click.thread_ts, UNREACHABLE.format(brand=_brand())
        )
    if response.status_code != 202:
        return _refused(service, click, (tenant_id, account_id, event_id), user_id, response)
    # Taken: the approval it leads to comes back to this thread (reply_ref), and is answered as whoever answers it.
    service.event_service.cache.cache_event_entry(
        click.thread_ts,
        {
            "event_id": None,
            "event_context": None,
            "text": payload["query"],
            "user_id": user_id,
            "account_id": account_id,
            "tenant_id": tenant_id,
            "is_thread": True,
            "alert_thread": True,
            "slack_user_id": click.user,
            "channel_id": click.channel,
            "team_id": click.team,
            "session_id": session_id,
            "platform": "slack",
            "app_id": click.app_id,
        },
    )
    sent = PR_SENT if kind == "pr" else PICK_SENT
    _tell(service, click.channel, click.team, click.user, click.thread_ts, sent.format(brand=_brand(), option=option))


# --- I made this change --------------------------------------------------------------------------------------


def _manual(service, click: Click) -> None:
    parsed = parse_plan_token(click.token)
    option = click.value.get("o")
    if not parsed or not isinstance(option, int) or option < 1:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)
    tenant_id, account_id, event_id, plan_version = parsed
    user_id = _linked(service, tenant_id, click.user)
    if not user_id:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, NOT_LINKED.format(brand=_brand()))
    request = {
        "account_id": account_id,
        "event_id": event_id,
        "step": option - 1,
        "slot": "manual",
        "plan_version": plan_version,
        "source": "slack",
    }
    try:
        response = requests.post(
            settings.services.llm_server + rt.EXECUTE_PATH,
            headers=_llm_headers(tenant_id, user_id),
            json={"input": {"request": request}},
            timeout=HTTP_TIMEOUT,
        )
    except requests.RequestException as e:
        LOG.warning("remediation manual: llm-server unreachable: %s", e)
        return _tell(
            service, click.channel, click.team, click.user, click.thread_ts, UNREACHABLE.format(brand=_brand())
        )
    if response.status_code != 200:
        return _refused(service, click, (tenant_id, account_id, event_id), user_id, response)
    _tell(service, click.channel, click.team, click.user, click.thread_ts, MANUAL_DONE.format(brand=_brand()))
    resync(tenant_id, account_id, event_id, user_id)


# --- Resolve -------------------------------------------------------------------------------------------------


def _resolve(service, click: Click) -> None:
    parsed = parse_run_token(click.token)
    if not parsed:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)
    tenant_id, account_id, event_id, run_id = parsed
    user_id = _linked(service, tenant_id, click.user)
    if not user_id:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, NOT_LINKED.format(brand=_brand()))
    try:
        response = requests.post(
            settings.services.api_server + TRIAGE_PATH,
            json={
                "action": {"name": RESOLVE_ACTION},
                "input": {"account_id": account_id, "event_id": event_id, "run_id": run_id},
                "session_variables": {"tenant_id": tenant_id, "user_id": user_id},
            },
            headers={"X-ACTION-TOKEN": settings.action_api_server_token},
            timeout=HTTP_TIMEOUT,
        )
    except requests.RequestException as e:
        LOG.warning("remediation resolve: api-server unreachable: %s", e)
        return _tell(
            service, click.channel, click.team, click.user, click.thread_ts, UNREACHABLE.format(brand=_brand())
        )
    if response.status_code == 409:
        message = _error_message(response)
        _tell(
            service,
            click.channel,
            click.team,
            click.user,
            click.thread_ts,
            f"This fix can't be confirmed any more: {message or 'the alert changed since'}",
        )
        return resync(tenant_id, account_id, event_id, user_id)
    if response.status_code != 200:
        return _refused(service, click, (tenant_id, account_id, event_id), user_id, response)
    # The verdict message shows who resolved it once the thread is drawn again from the event's state.
    _tell(service, click.channel, click.team, click.user, click.thread_ts, RESOLVED)
    resync(tenant_id, account_id, event_id, user_id)


# --- modals --------------------------------------------------------------------------------------------------


def _open_modal(service, click: Click, view: dict) -> None:
    """views.open with the click's trigger_id, before anything else is read (it lasts 3 seconds)."""
    try:
        bot = service.common_service.get_slack_installation(click.team)
        service.slack_app.client.views_open(token=bot.token, trigger_id=click.trigger_id, view=view)
    except SlackApiError as e:
        LOG.info("remediation modal not opened: %s", rt.slack_error(e) or e)
        text = TRIGGER_EXPIRED if rt.slack_error(e) in ("expired_trigger_id", "invalid_trigger_id") else CANT_READ
        _tell(service, click.channel, click.team, click.user, click.thread_ts, text)
    except Exception:
        LOG.warning("remediation modal not opened", exc_info=True)
        _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)


def _note_block(label="Add a note", hint=None) -> dict:
    block = {
        "type": "input",
        "block_id": "note",
        "optional": True,
        "label": {"type": "plain_text", "text": label},
        "element": {"type": "plain_text_input", "action_id": "note", "multiline": True, "max_length": NOTE_LIMIT},
    }
    if hint:
        block["hint"] = {"type": "plain_text", "text": hint}
    return block


def _private(click: Click, **extra) -> str:
    return json.dumps(
        {
            "c": click.channel,
            "t": click.thread_ts,
            "m": click.message_ts,
            "team": click.team,
            "app": click.app_id,
            "token": click.token,
            "fp": click.main_fingerprint,
            **extra,
        },
        separators=(",", ":"),
    )


def replan_view(click: Click) -> dict:
    return {
        "type": "modal",
        "callback_id": CALLBACK_REPLAN,
        "private_metadata": _private(click),
        "title": {"type": "plain_text", "text": "Other fixes"},
        "submit": {"type": "plain_text", "text": "Ask for other fixes"},
        "close": {"type": "plain_text", "text": "Cancel"},
        "blocks": [
            {
                "type": "input",
                "block_id": "reasons",
                "optional": True,
                "label": {"type": "plain_text", "text": "What's wrong with these fixes?"},
                "element": {
                    "type": "checkboxes",
                    "action_id": "reasons",
                    "options": [{"text": {"type": "plain_text", "text": r}, "value": r} for r in REPLAN_REASONS],
                },
            },
            _note_block("Note", hint="Pick a reason or write a note."),
        ],
    }


ANSWER_TITLES = {
    "yes": ("Approve this step", "Approve"),
    "no": ("Deny this step", "Deny"),
    "norecheck": ("Deny and re-check", "Deny"),
}


def answer_view(click: Click, answer: str) -> dict:
    title, submit = ANSWER_TITLES[answer]
    return {
        "type": "modal",
        "callback_id": CALLBACK_ANSWER,
        "private_metadata": _private(click, a=answer),
        "title": {"type": "plain_text", "text": title},
        "submit": {"type": "plain_text", "text": submit},
        "close": {"type": "plain_text", "text": "Cancel"},
        "blocks": [_note_block()],
    }


def _open_replan(service, click: Click) -> None:
    _open_modal(service, click, replan_view(click))


def _open_answer(service, click: Click) -> None:
    answer = str(click.value.get("a") or "")
    if answer not in ANSWER_TITLES:
        return _tell(service, click.channel, click.team, click.user, click.thread_ts, CANT_READ)
    _open_modal(service, click, answer_view(click, answer))


# --- modal submissions ---------------------------------------------------------------------------------------


def _values(view) -> dict:
    out = {}
    for block in ((view.get("state") or {}).get("values") or {}).values():
        for action_id, element in (block or {}).items():
            if element.get("type") == "checkboxes":
                out[action_id] = [o.get("value") for o in element.get("selected_options") or [] if o.get("value")]
            else:
                out[action_id] = element.get("value")
    return out


def handle_view_submission(service, data) -> None:
    view = data.get("view") or {}
    try:
        private = json.loads(view.get("private_metadata") or "{}")
    except ValueError:
        private = {}
    values = _values(view)
    user = (data.get("user") or {}).get("id")
    service.common_service.app_id = private.get("app") or data.get("api_app_id")
    note = str(values.get("note") or "").strip()[:NOTE_LIMIT]
    if view.get("callback_id") == CALLBACK_REPLAN:
        return _submit_replan(service, private, user, values.get("reasons") or [], note)
    return _submit_answer(service, private, user, note)


def _submit_replan(service, private, user, reasons, note) -> None:
    channel, team, thread_ts = private.get("c"), private.get("team"), private.get("t")
    parsed = parse_plan_token(private.get("token"))
    if not parsed:
        return _tell(service, channel, team, user, thread_ts, CANT_READ)
    tenant_id, account_id, event_id, _ = parsed
    user_id = _linked(service, tenant_id, user)
    if not user_id:
        return _tell(service, channel, team, user, thread_ts, NOT_LINKED.format(brand=_brand()))
    request = {"account_id": account_id, "event_id": event_id, "mode": "replan", "reasons": reasons, "note": note}
    try:
        response = requests.post(
            settings.services.llm_server + PLAN_PATH,
            headers=_llm_headers(tenant_id, user_id),
            json={"input": {"request": request}},
            timeout=HTTP_TIMEOUT,
        )
    except requests.RequestException as e:
        LOG.warning("remediation replan: llm-server unreachable: %s", e)
        return _tell(service, channel, team, user, thread_ts, UNREACHABLE.format(brand=_brand()))
    if response.status_code >= 300:
        message = _error_message(response)
        _tell(service, channel, team, user, thread_ts, f"Not asked: {message or 'the request was refused'}")
    else:
        _tell(service, channel, team, user, thread_ts, REPLAN_SENT.format(brand=_brand()))
    resync(tenant_id, account_id, event_id, user_id)


def answer_outcome_text(answer: str, slack_user: str, note: str) -> str:
    """The approval card's text once answered: who, how, and the note as they wrote it."""
    verb = "Approved" if answer == "yes" else "Denied" if answer == "no" else "Denied, re-check asked,"
    text = f"*{verb} by <@{slack_user}> in Slack*"
    if note:
        text += f": “{note}”"
    return text


def _submit_answer(service, private, user, note) -> None:
    """The approval card's answer, through the conversation's existing follow-up answer, with the note."""
    channel, team, thread_ts, card_ts = private.get("c"), private.get("team"), private.get("t"), private.get("m")
    answer = private.get("a")
    if answer not in ANSWER_TITLES:
        return _tell(service, channel, team, user, thread_ts, CANT_READ)
    cache = service.event_service.cache
    entry = cache.get_event_entry(thread_ts) if thread_ts else None
    # Only the approval this card asks: answered, retired, or a later card's question, it is not answered here.
    if not entry or not entry.get("alert_thread") or entry.get("followup_msg_ts") != card_ts:
        _tell(service, channel, team, user, thread_ts, ANSWER_GONE)
        return
    # Deny and re-check sends a plain "no": the server queues the re-check when the investigation is newer than the
    # plan (PR 1), for every surface.
    response_option = "yes" if answer == "yes" else "no"
    service.event_service._submit_followup(
        entry,
        channel,
        team,
        thread_ts,
        user,
        response_option,
        reason=note or None,
        replacement_message=answer_outcome_text(answer, user, note),
    )


# --- the per-step Run / Decline of before --------------------------------------------------------------------


def retire_step_buttons(service, channel_id, team_id, slack_user_id, message, ids=None, background_tasks=None) -> None:
    """A Run or Decline on a step message posted before the loop drew the thread this way: refused politely, and
    the message loses its buttons. The server refuses such a Run too (PR 1); this spares the click its long wait.
    ids (tenant, account, event): the thread is drawn again, so the fix options the reply points to are there; a
    thread posted before the loop drew it has none until its state next changes."""
    text = RETIRED.format(brand=_brand())
    blocks = [b for b in message.get("blocks") or [] if b.get("type") != "actions"]
    if blocks:
        blocks.append({"type": "context", "elements": [{"type": "mrkdwn", "text": "_These buttons were retired._"}]})
        service.common_service.update_slack_message_with_blocks(channel_id, team_id, message.get("ts"), blocks)
    _tell(service, channel_id, team_id, slack_user_id, message.get("thread_ts") or message.get("ts"), text)
    if ids:
        tenant_id, account_id, event_id = ids
        user_id = _linked(service, tenant_id, slack_user_id)
        if background_tasks is not None:
            background_tasks.add_task(resync, tenant_id, account_id, event_id, user_id)
        else:
            resync(tenant_id, account_id, event_id, user_id)
