"""Buttons on Microsoft Teams cards: the signed data they carry, the stored copy
of each card that a click updates, and the actions themselves.

A click is an Action.Submit: Teams sends it as a message activity whose value
holds the button's data, with replyToId naming the card. Teams can't return a
posted card, so the card each button sits on is stored in sent_notifications
when it's sent; a click locks that row, stamps the stored card and writes the
result back to Teams."""

import asyncio
import json
import logging
import threading
import time
import uuid
from datetime import datetime, timedelta, timezone
from urllib.parse import quote
from typing import Callable, Optional

import requests
from sqlalchemy.orm import Session

from notifications_server import sync_engine
from notifications_server.configs.settings import URLRoutes, settings
from notifications_server.models.models import SentNotifications
from notifications_server.services.cache import Cache
from notifications_server.services.messaging_installations import ms_teams_aad_tenant_owner
from notifications_server.services.ms_teams.bot_sender import TeamsBotError, card_activity, get_sender
from notifications_server.utils.action_requests import ActionRequestBody, sign_action_request, verify_action_request
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

ACTION_KEY = "nb_action"
SUPPRESS = "suppress"
APPROVE = "approve"
APPROVAL_DATA = "nb_approval"
APPROVAL_CHOICE = "nb_approval_choice"
# Prefix of the ids of a card's approval controls.
APPROVAL_ID = "nb_approval"
MAX_APPROVAL_BUTTONS = 3
SUPPRESS_CHOICE = "nb_suppress_choice"
SUPPRESS_ACTION_ID = "nb_suppress"
STATUS_ID = "nb_status"
ASK_NUBI = "ask_nubi"
ASK_NUBI_DATA = "nb_ask_nubi"
ASK_NUBI_ID = "nb_ask_nubi"
# Ask Nubi's own status line, so it and Suppress's never erase each other.
ANALYSIS_STATUS_ID = "nb_analysis_status"
FEEDBACK = "feedback"
FEEDBACK_DATA = "nb_feedback"
FEEDBACK_ID = "nb_feedback"
# Analysis text past this is cut, with a link to the event in NudgeBee.
WRITEUP_LIMIT = 20_000
# Sections posted to the card's thread as they complete, in order.
ANALYSIS_SECTIONS = (
    ("summary", "Here's a quick summary while I keep digging:"),
    ("investigation", "Here's what I'm finding on the likely root cause:"),
)
ANALYSIS_MAX_POLL_FAILURES = 3
# Starting an analysis can take much of a minute; later polls are quick reads.
ANALYSIS_KICKOFF_TIMEOUT_SECONDS = 90

# Identifies the signing key, so a later key can be verified alongside this one.
KEY_ID = "1"
# How long a card's buttons stay valid. api-server's sent_notifications cleanup
# keeps a Teams card's row exactly this long (teamsCardRetentionDays).
MAX_AGE_SECONDS = 7 * 24 * 3600
# Teams refuses a message over about 28 KB; buttons are only added to a card
# that stays under this with them.
CARD_SIZE_LIMIT = 25_000
CLAIM_SECONDS = 120
# An action still marked in progress after this long is treated as abandoned
# (e.g. the pod handling it stopped), so it can be run again.
IN_PROGRESS_STALE_SECONDS = 600
IN_PROGRESS, DONE = "in_progress", "done"
REFUSED = object()  # the action already ran on this card
BUSY = object()  # the action is running on this card now

SUPPRESS_THIS_DURATIONS = (("1 hour", 1), ("4 hours", 4), ("24 hours", 24), ("7 days", 168))
SUPPRESS_ALL_DURATIONS = (("4 hours", 4), ("24 hours", 24), ("7 days", 168))
PICK_A_DURATION = "Pick how long to suppress the alert for, then press Suppress."
ALREADY_SUPPRESSED = "This alert was already suppressed from this card."
STILL_SUPPRESSING = "Still suppressing this alert. It'll show on the card in a moment."
SUPPRESS_CARD_TOO_OLD = (
    "This card is more than 7 days old, so its Suppress menu no longer works."
    " Suppress the alert from Triage Rules in NudgeBee."
)

PICK_AN_OPTION = "Pick an option, then press Submit."
APPROVAL_ANSWERED = "This approval was already answered."
STILL_RECORDING = "Still recording the decision on this approval."
APPROVAL_EXPIRED = "This approval request has expired."
APPROVAL_FAILED = "Couldn't record the decision. Please try again."
STILL_ANALYZING = "Still analyzing. Results will appear in this card's thread."
ANALYSIS_FAILED = "Something went wrong analyzing this. Try again in a moment."
ANALYSIS_BUSY = "Nubi is analyzing a lot of alerts right now. Try again in a minute."
FEEDBACK_ALREADY = "This answer was already rated."
FEEDBACK_FAILED = "Couldn't record your feedback. Please try again."
FEEDBACK_CARD_TOO_OLD = "This answer is too old to rate."
ASK_NUBI_CARD_TOO_OLD = (
    "This card is more than 7 days old, so its buttons no longer work." " Ask Nubi about the event from NudgeBee."
)
APPROVAL_CARD_TOO_OLD = (
    "This card is more than 7 days old, so its buttons no longer work."
    " Answer the approval from the automation's run in NudgeBee."
)


def buttons_enabled() -> bool:
    return bool(settings.ms_teams.action_signing_key)


# ------------------------------- signed data -------------------------------


def sign(action_name: str, params: dict) -> str:
    body = ActionRequestBody(
        timestamp=int(time.time()), action_name=action_name, action_params=params, origin="ms_teams"
    )
    signature = sign_action_request(body, settings.ms_teams.action_signing_key)
    return json.dumps({"kid": KEY_ID, "body": body.model_dump(exclude_none=True), "signature": signature})


def _checked(raw, action_name: str, tenant_id):
    """(params, age in seconds) of button data we signed for this action and this
    NudgeBee tenant, or None."""
    try:
        payload = json.loads(raw) if isinstance(raw, str) else raw
        if payload.get("kid") != KEY_ID:
            return None
        body = ActionRequestBody(**(payload.get("body") or {}))
        if body.action_name != action_name:
            return None
        if not verify_action_request(body, payload.get("signature") or "", settings.ms_teams.action_signing_key):
            return None
    except Exception:
        return None
    params = body.action_params or {}
    if not tenant_id or str(params.get("tenant_id")) != str(tenant_id):
        return None
    return params, time.time() - body.timestamp


def verify(raw, action_name: str, tenant_id) -> Optional[dict]:
    """The params of signed button data, or None unless it was signed by us for
    this action and this NudgeBee tenant within MAX_AGE_SECONDS."""
    checked = _checked(raw, action_name, tenant_id)
    if not checked or not -300 <= checked[1] <= MAX_AGE_SECONDS:
        return None
    return checked[0]


def expired(raw, action_name: str, tenant_id) -> bool:
    """True for genuine button data that is too old to use."""
    checked = _checked(raw, action_name, tenant_id)
    return bool(checked) and checked[1] > MAX_AGE_SECONDS


def fits(card: dict) -> bool:
    return len(json.dumps(card).encode()) <= CARD_SIZE_LIMIT


# ------------------------------- card content -------------------------------


def suppress_action(finding: dict, tenant_id) -> Optional[dict]:
    """ "Suppress…" for a finding card: a time-boxed suppression of this alert, or
    of every alert with its aggregation key. Each choice is signed on its own,
    since a choice set's value is user input, not part of the button's data."""
    account_id = finding.get("cloud_account_id")
    if not (buttons_enabled() and account_id and tenant_id):
        return None
    fingerprint = str(finding.get("fingerprint") or "").strip()
    alertname = str(finding.get("aggregation_key") or "").strip()

    def choices(scope, durations, label):
        return [
            {
                "title": f"{label} · {text}",
                "value": sign(
                    SUPPRESS,
                    {
                        "event_id": finding.get("id"),
                        "fingerprint": fingerprint,
                        "alertname": alertname,
                        "account_id": account_id,
                        "tenant_id": str(tenant_id),
                        "scope": scope,
                        "duration_hours": hours,
                    },
                ),
            }
            for text, hours in durations
        ]

    options = []
    if fingerprint:
        options += choices("fingerprint", SUPPRESS_THIS_DURATIONS, "This alert")
    if alertname:
        shown = alertname if len(alertname) <= 30 else alertname[:27] + "…"
        options += choices("alertname", SUPPRESS_ALL_DURATIONS, f'All "{shown}" alerts')
    if not options:
        return None
    return {
        "type": "Action.ShowCard",
        "id": SUPPRESS_ACTION_ID,
        "title": "Suppress…",
        "card": {
            "type": "AdaptiveCard",
            "body": [
                {
                    "type": "Input.ChoiceSet",
                    "id": SUPPRESS_CHOICE,
                    "style": "compact",
                    "isRequired": True,
                    "errorMessage": "Pick how long to suppress for",
                    "choices": options,
                }
            ],
            "actions": [{"type": "Action.Submit", "title": "Suppress", "data": {ACTION_KEY: SUPPRESS}}],
        },
    }


def _label(option: str) -> str:
    return option.replace("_", " ").replace("-", " ").title()


def with_approval_buttons(card: dict, params, tenant_id) -> dict:
    """The card with a workflow approval's options as buttons (up to three) or a
    choice set, when params carry an approval token. Each option's data, token
    included, is signed."""
    token = getattr(params, "approval_token", None)
    options = [o for o in getattr(params, "approval_options", None) or [] if isinstance(o, str) and o.strip()]
    if not (buttons_enabled() and token and options and tenant_id):
        return card

    def signed(option):
        return sign(APPROVE, {"tenant_id": str(tenant_id), "token": token, "status": option})

    body = list(card.get("body") or [])
    if len(options) <= MAX_APPROVAL_BUTTONS:
        added = [
            {
                "type": "Action.Submit",
                "id": f"{APPROVAL_ID}_{i}",
                "title": _label(option),
                "data": {ACTION_KEY: APPROVE, APPROVAL_DATA: signed(option)},
            }
            for i, option in enumerate(options)
        ]
    else:
        body.append(
            {
                "type": "Input.ChoiceSet",
                "id": APPROVAL_CHOICE,
                "style": "compact",
                "isRequired": True,
                "errorMessage": "Pick an option",
                "choices": [{"title": _label(option), "value": signed(option)} for option in options],
            }
        )
        added = [
            {"type": "Action.Submit", "id": f"{APPROVAL_ID}_submit", "title": "Submit", "data": {ACTION_KEY: APPROVE}}
        ]
    with_buttons = {**card, "body": body, "actions": [*(card.get("actions") or []), *added]}
    # The prompt must still be delivered when its message leaves no room for the buttons.
    if not fits(with_buttons):
        LOG.info("Teams approval card is too large for its buttons")
        return card
    return with_buttons


def ask_nubi_action(finding: dict, tenant_id) -> Optional[dict]:
    """ "Ask Nubi" for a finding card: analyse the event and post the findings in
    the card's thread."""
    event_id = finding.get("id")
    account_id = finding.get("cloud_account_id")
    if not (buttons_enabled() and event_id and account_id and tenant_id):
        return None
    params = {"event_id": event_id, "account_id": account_id, "tenant_id": str(tenant_id)}
    return {
        "type": "Action.Submit",
        "id": ASK_NUBI_ID,
        "title": "Ask Nubi",
        "data": {ACTION_KEY: ASK_NUBI, ASK_NUBI_DATA: sign(ASK_NUBI, params)},
    }


def has_actions(card: dict) -> bool:
    ids = [a.get("id") or "" for a in card.get("actions") or []]
    return any(i in (SUPPRESS_ACTION_ID, ASK_NUBI_ID) or i.startswith(APPROVAL_ID) for i in ids)


def with_analysis_status(card: dict, text: Optional[str]) -> dict:
    """The card with Ask Nubi's status line set to text, or removed when None."""
    body = [item for item in card.get("body") or [] if item.get("id") != ANALYSIS_STATUS_ID]
    if text:
        body.append({"type": "TextBlock", "id": ANALYSIS_STATUS_ID, "text": text, "wrap": True, "isSubtle": True})
    return {**card, "body": body}


def without_approval(card: dict) -> dict:
    return {
        **card,
        "body": [b for b in card.get("body") or [] if b.get("id") != APPROVAL_CHOICE],
        "actions": [a for a in card.get("actions") or [] if not (a.get("id") or "").startswith(APPROVAL_ID)],
    }


def with_status(card: dict, text: Optional[str]) -> dict:
    """The card with its status line set to text, or removed when text is None."""
    body = [item for item in card.get("body") or [] if item.get("id") != STATUS_ID]
    if text:
        body.append({"type": "TextBlock", "id": STATUS_ID, "text": text, "wrap": True, "isSubtle": True})
    return {**card, "body": body}


def feedback_actions(tenant_id, account_id, answer_id, session_id) -> list:
    """👍/👎 buttons for a chat answer, or none without the signing key.
    answer_id keys the feedback, so each answer in a thread is rated on its own;
    session_id is the thread's Nubi session."""
    if not buttons_enabled():
        return []
    params = {"tenant_id": str(tenant_id), "account_id": str(account_id), "answer_id": answer_id}
    return [
        {
            "type": "Action.Submit",
            "id": f"{FEEDBACK_ID}_{key}",
            "title": title,
            "data": {
                ACTION_KEY: FEEDBACK,
                FEEDBACK_DATA: sign(FEEDBACK, {**params, "session_id": session_id, "useful": useful}),
            },
        }
        for key, title, useful in (("up", "👍 Helpful", True), ("down", "👎 Not helpful", False))
    ]


def without_feedback(card: dict) -> dict:
    return {
        **card,
        "actions": [a for a in card.get("actions") or [] if not (a.get("id") or "").startswith(FEEDBACK_ID)],
    }


def without_action(card: dict, action_id: str) -> dict:
    return {**card, "actions": [a for a in card.get("actions") or [] if a.get("id") != action_id]}


# ------------------------------- stored card -------------------------------


def card_row(tenant_id, account_id, fingerprint, channel_id, message_id, card, kind) -> Optional[SentNotifications]:
    """The sent_notifications row for a bot card, returned by the sender and
    committed with the rest of the notification. None without a message id,
    since a click or a thread reply finds its card by that id."""
    if not (message_id and channel_id):
        return None
    return SentNotifications(
        created_at=utc_now(),
        tenant_id=tenant_id,
        account_id=account_id or None,
        fingerprint=fingerprint or f"ms_teams:{message_id}",
        teams_channel_id=channel_of(channel_id),
        teams_message_id=message_id,
        teams_metadata=json.dumps({"kind": kind, "card": card, "actions": {}}),
    )


def store(row: SentNotifications) -> None:
    """Commit a card row on its own, for cards sent outside a notification batch."""
    with Session(sync_engine) as session:
        session.add(row)
        session.commit()


def stored_card(tenant_id, channel_id, message_id) -> Optional[dict]:
    """The stored card a message id names, as {kind, card, account_id}, or None."""
    with Session(sync_engine) as session:
        row = (
            session.query(SentNotifications.account_id, SentNotifications.teams_metadata)
            .filter(
                SentNotifications.tenant_id == tenant_id,
                SentNotifications.teams_channel_id == channel_id,
                SentNotifications.teams_message_id == message_id,
            )
            .first()
        )
    if row is None:
        return None
    meta = json.loads(row.teams_metadata or "{}")
    return {
        "kind": meta.get("kind"),
        "card": meta.get("card") or {},
        "account_id": str(row.account_id) if row.account_id else None,
    }


def card_text(card: dict) -> str:
    """A card's readable text, one line per block or fact, without its status lines."""
    lines = []

    def walk(items):
        if not isinstance(items, list):
            return
        for item in items:
            if not isinstance(item, dict) or item.get("id") in (STATUS_ID, ANALYSIS_STATUS_ID):
                continue
            if item.get("type") == "TextBlock" and item.get("text"):
                lines.append(str(item["text"]))
            elif item.get("type") == "FactSet":
                lines.extend(
                    f"{f.get('title', '')}: {f.get('value', '')}"
                    for f in item.get("facts") or []
                    if isinstance(f, dict)
                )
            walk(item.get("items"))
            walk(item.get("columns"))

    walk(card.get("body"))
    return "\n".join(lines)


def thread_root_of(conversation_id: str) -> Optional[str]:
    """The id of the message a thread hangs off, or None outside a thread."""
    _, sep, root = (conversation_id or "").partition(";messageid=")
    return root if sep and root else None


def channel_of(conversation_id: str) -> str:
    """The channel a click came from: its conversation id without the thread suffix."""
    return (conversation_id or "").split(";messageid=")[0]


def _blocking(entry):
    """REFUSED or BUSY when a recorded action state blocks running it again."""
    if not isinstance(entry, dict):
        return None
    if entry.get("state") == DONE:
        return REFUSED
    if entry.get("state") == IN_PROGRESS and time.time() - entry.get("at", 0) < IN_PROGRESS_STALE_SECONDS:
        return BUSY
    return None


def _stamp(card_ref, action, state, mutate: Callable[[dict], dict], refuse_if_started: bool):
    """Lock the card's row, apply mutate to the stored card and record the
    action's state, then commit. card_ref is (tenant_id, channel_id, message_id).
    Returns the new card, None when there is no stored card, or REFUSED / BUSY
    when the action already ran or is running."""
    tenant_id, channel_id, message_id = card_ref
    with Session(sync_engine) as session:
        row = (
            session.query(SentNotifications)
            .filter(
                SentNotifications.tenant_id == tenant_id,
                SentNotifications.teams_channel_id == channel_id,
                SentNotifications.teams_message_id == message_id,
            )
            .with_for_update()
            .first()
        )
        if row is None:
            return None
        meta = json.loads(row.teams_metadata or "{}")
        actions = meta.setdefault("actions", {})
        blocked = _blocking(actions.get(action)) if refuse_if_started else None
        if blocked:
            return blocked
        if state is None:
            actions.pop(action, None)
        else:
            actions[action] = {"state": state, "at": int(time.time())}
        meta["card"] = mutate(meta.get("card") or {})
        row.teams_metadata = json.dumps(meta)
        session.commit()
        return meta["card"]


def begin(card_ref, action, mutate):
    return _stamp(card_ref, action, IN_PROGRESS, mutate, refuse_if_started=True)


def finish(card_ref, action, state, mutate):
    return _stamp(card_ref, action, state, mutate, refuse_if_started=False)


async def _show(activity, card: dict) -> None:
    """Write the stamped card back to Teams. A failure is logged; the outcome
    also goes to the card's thread."""
    try:
        await asyncio.to_thread(
            get_sender().update,
            activity.service_url,
            activity.conversation.id,
            activity.reply_to_id,
            card_activity(card),
        )
    except TeamsBotError as e:
        LOG.warning("Updating Teams card %s failed: %s", activity.reply_to_id, e)


# ------------------------------- clicks -------------------------------


def is_card_action(activity) -> bool:
    return isinstance(activity.value, dict) and bool(activity.value.get(ACTION_KEY))


def _tenant_for(aad_tenant_id):
    with Session(sync_engine) as session:
        return ms_teams_aad_tenant_owner(session, aad_tenant_id, enabled_only=True)


async def handle(common_service, activity, user_email, aad_tenant_id) -> None:
    action = activity.value.get(ACTION_KEY)
    if action == SUPPRESS:
        await _suppress(common_service, activity, user_email, aad_tenant_id)
    elif action == ASK_NUBI:
        await _ask_nubi(common_service, activity, user_email, aad_tenant_id)
    elif action == APPROVE:
        await _approve(common_service, activity, user_email, aad_tenant_id)
    elif action == FEEDBACK:
        await _feedback(common_service, activity, user_email, aad_tenant_id)
    else:
        LOG.info("Ignoring unknown Teams card action %s", action)


async def _suppress(common_service, activity, user_email, aad_tenant_id) -> None:
    # Imported here: actions_common imports events, which imports this module.
    from notifications_server.services.actions import validate_and_get_user_tenants
    from notifications_server.services.actions_common import (
        SUPPRESS_NOT_ALLOWED_MESSAGE,
        UNABLE_TO_PROCESS_REQUEST,
        USER_NOT_FOUND_MESSAGE,
        can_manage_triage_rules,
    )

    async def reply(text):
        await common_service.teams_reply(activity, text, aad_tenant_id)

    raw = activity.value.get(SUPPRESS_CHOICE)
    if not raw:
        return await reply(PICK_A_DURATION)
    tenant_id = await asyncio.to_thread(_tenant_for, aad_tenant_id)
    params = verify(raw, SUPPRESS, tenant_id)
    duration_hours = int((params or {}).get("duration_hours") or 0)
    if not (params and params.get("account_id") and params.get("scope") and duration_hours > 0):
        if expired(raw, SUPPRESS, tenant_id):
            return await reply(SUPPRESS_CARD_TOO_OLD)
        LOG.warning("Rejecting a Teams suppress click with invalid data")
        return await reply(UNABLE_TO_PROCESS_REQUEST)
    account_id = params["account_id"]

    user_id, tenants = await asyncio.to_thread(validate_and_get_user_tenants, user_email)
    if not user_id or str(tenant_id) not in {str(t) for t in tenants or []}:
        return await reply(USER_NOT_FOUND_MESSAGE)
    if not await asyncio.to_thread(can_manage_triage_rules, user_id, str(tenant_id), account_id):
        return await reply(SUPPRESS_NOT_ALLOWED_MESSAGE)

    message_id = activity.reply_to_id
    channel_id = channel_of(activity.conversation.id if activity.conversation else "")
    if not (message_id and channel_id):
        LOG.warning("Rejecting a Teams suppress click that doesn't name its card")
        return await reply(UNABLE_TO_PROCESS_REQUEST)
    card_ref = (tenant_id, channel_id, message_id)
    claim = f"{tenant_id}:{message_id}:{SUPPRESS}"
    cache = Cache()
    # The claim covers the action while it runs; once it's done, the stored card
    # refuses repeats.
    if not cache.claim_card_action(claim, CLAIM_SECONDS):
        return await reply(STILL_SUPPRESSING)
    try:
        await _suppress_claimed(activity, reply, card_ref, params, tenant_id, user_id, user_email)
    except Exception as e:
        LOG.exception("Suppressing from Teams failed: %s", e)
        await reply(UNABLE_TO_PROCESS_REQUEST)
    finally:
        cache.release_card_action(claim)


async def _suppress_claimed(activity, reply, card_ref, params, tenant_id, user_id, user_email) -> None:
    """The suppression itself, once the click is checked and claimed."""
    from notifications_server.services.actions_common import (
        SUPPRESS_DURATION_LABELS,
        UNABLE_TO_PROCESS_REQUEST,
        build_suppress_rule_input,
        create_suppress_rule,
    )

    account_id = params["account_id"]
    duration_hours = int(params["duration_hours"])
    started = await asyncio.to_thread(begin, card_ref, SUPPRESS, lambda c: with_status(c, "Suppressing…"))
    if started is REFUSED:
        return await reply(ALREADY_SUPPRESSED)
    if started is BUSY:
        return await reply(STILL_SUPPRESSING)
    if started is not None:
        await _show(activity, started)

    until = datetime.now(timezone.utc) + timedelta(hours=duration_hours)
    rule_input = build_suppress_rule_input(
        account_id,
        params["scope"],
        params.get("fingerprint"),
        params.get("alertname"),
        until,
        f"Suppressed via Microsoft Teams by {user_email}",
    )
    try:
        created = bool(rule_input) and await asyncio.to_thread(
            create_suppress_rule, str(tenant_id), user_id, rule_input
        )
    except Exception as e:
        LOG.warning("Creating a suppression from Teams failed: %s", e)
        created = False
    if not created:
        card = await asyncio.to_thread(finish, card_ref, SUPPRESS, None, lambda c: with_status(c, None))
        if card:
            await _show(activity, card)
        return await reply(UNABLE_TO_PROCESS_REQUEST)

    clicker = (activity.from_property.name if activity.from_property else None) or user_email
    scope_label = "this alert" if params["scope"] == "fingerprint" else f'all "{params.get("alertname")}" alerts'
    duration_label = SUPPRESS_DURATION_LABELS.get(duration_hours, f"{duration_hours}h")
    until_text = until.strftime("%d %b %Y %H:%M UTC")
    status = f"Suppressed ({scope_label} · {duration_label}) by {clicker} · until {until_text}"
    card = await asyncio.to_thread(
        finish,
        card_ref,
        SUPPRESS,
        DONE,
        lambda c: with_status(without_action(c, SUPPRESS_ACTION_ID), status),
    )
    if card:
        await _show(activity, card)
    triage_rules_url = settings.urls.cluster_details_url(
        account_id, utm_source=URLRoutes.UTMSource.TEAMS, anchor=URLRoutes.Anchors.EVENTS_TRIAGE_RULES
    )
    await reply(
        f"{clicker} suppressed {scope_label} for {duration_label}. Repeat alerts won't notify until"
        f" {until_text}. [Open Triage Rules]({triage_rules_url}) to undo."
    )


def _post_approval(token: str, status: str) -> Optional[int]:
    """The workflow server's HTTP status for the decision, or None when it
    couldn't be reached."""
    try:
        # The token comes from signed data and is colon-joined ids; it's still
        # escaped so nothing in it can change the path.
        url = f"{settings.services.workflow_server.rstrip('/')}/approvals/{quote(token, safe=':')}"
        response = requests.post(url, json={"status": status}, timeout=30)
        return response.status_code
    except requests.RequestException as e:
        LOG.warning("Recording a Teams approval decision failed: %s", e)
        return None


async def _approve(common_service, activity, user_email, aad_tenant_id) -> None:
    # Imported here: actions_common imports events, which imports this module.
    from notifications_server.services.actions import validate_and_get_user_tenants
    from notifications_server.services.actions_common import UNABLE_TO_PROCESS_REQUEST, USER_NOT_FOUND_MESSAGE

    async def reply(text):
        await common_service.teams_reply(activity, text, aad_tenant_id)

    raw = activity.value.get(APPROVAL_DATA) or activity.value.get(APPROVAL_CHOICE)
    if not raw:
        return await reply(PICK_AN_OPTION)
    tenant_id = await asyncio.to_thread(_tenant_for, aad_tenant_id)
    params = verify(raw, APPROVE, tenant_id)
    if not (params and params.get("token") and params.get("status")):
        if expired(raw, APPROVE, tenant_id):
            return await reply(APPROVAL_CARD_TOO_OLD)
        LOG.warning("Rejecting a Teams approval click with invalid data")
        return await reply(UNABLE_TO_PROCESS_REQUEST)

    # The approval token is the authority to decide, as on Slack; the clicker
    # must also be a user of the workspace the approval belongs to.
    user_id, tenants = await asyncio.to_thread(validate_and_get_user_tenants, user_email)
    if not user_id or str(tenant_id) not in {str(t) for t in tenants or []}:
        return await reply(USER_NOT_FOUND_MESSAGE)

    message_id = activity.reply_to_id
    channel_id = channel_of(activity.conversation.id if activity.conversation else "")
    if not (message_id and channel_id):
        LOG.warning("Rejecting a Teams approval click that doesn't name its card")
        return await reply(UNABLE_TO_PROCESS_REQUEST)
    card_ref = (tenant_id, channel_id, message_id)
    claim = f"{tenant_id}:{message_id}:{APPROVE}"
    cache = Cache()
    # The claim covers the decision while it's recorded; once it's done, the
    # stored card refuses repeats.
    if not cache.claim_card_action(claim, CLAIM_SECONDS):
        return await reply(STILL_RECORDING)
    try:
        await _approve_claimed(activity, reply, card_ref, params, user_email)
    except Exception as e:
        LOG.exception("Recording a Teams approval failed: %s", e)
        await reply(APPROVAL_FAILED)
    finally:
        cache.release_card_action(claim)


async def _approve_claimed(activity, reply, card_ref, params, user_email) -> None:
    """Records the decision, once the click is checked and claimed."""
    started = await asyncio.to_thread(begin, card_ref, APPROVE, lambda c: with_status(c, "Recording your decision…"))
    if started is REFUSED:
        return await reply(APPROVAL_ANSWERED)
    if started is BUSY:
        return await reply(STILL_RECORDING)
    if started is not None:
        await _show(activity, started)

    status_code = await asyncio.to_thread(_post_approval, params["token"], params["status"])
    clicker = (activity.from_property.name if activity.from_property else None) or user_email
    if status_code is not None and status_code < 300:
        outcome = f"{clicker} chose **{_label(params['status'])}**"
    elif status_code == 409:
        outcome = APPROVAL_ANSWERED
    elif status_code == 404:
        outcome = APPROVAL_EXPIRED
    else:
        card = await asyncio.to_thread(finish, card_ref, APPROVE, None, lambda c: with_status(c, None))
        if card:
            await _show(activity, card)
        return await reply(APPROVAL_FAILED)

    card = await asyncio.to_thread(finish, card_ref, APPROVE, DONE, lambda c: with_status(without_approval(c), outcome))
    if card:
        await _show(activity, card)
    await reply(outcome)


# ------------------------------- Ask Nubi -------------------------------

_active_analyses = 0
_analyses_lock = threading.Lock()


def _analysis_claim_ttl(kickoff: bool = False) -> int:
    """Outlasts one poll (the call's timeout plus the wait between polls), so a
    live worker's claim never lapses, yet a worker that dies (e.g. in a deploy)
    frees the event within a minute or two. Refreshed before each call and each
    wait."""
    slack = settings.slack
    timeout = ANALYSIS_KICKOFF_TIMEOUT_SECONDS if kickoff else slack.event_analysis_poll_timeout_seconds
    return int(timeout + slack.event_analysis_poll_seconds + 30)


def _stamp_analysis(card_ref, text):
    """Set Ask Nubi's line on the stored card. Ask Nubi is safe to repeat, so
    it records no state that refuses a click."""
    return finish(card_ref, ASK_NUBI, None, lambda c: with_analysis_status(c, text))


async def _ask_nubi(common_service, activity, user_email, aad_tenant_id) -> None:
    # Imported here: actions_common imports events, which imports this module.
    from notifications_server.services.actions import validate_and_get_user_tenants
    from notifications_server.services.actions_common import UNABLE_TO_PROCESS_REQUEST, USER_NOT_FOUND_MESSAGE

    global _active_analyses

    async def reply(text):
        await common_service.teams_reply(activity, text, aad_tenant_id)

    raw = activity.value.get(ASK_NUBI_DATA)
    tenant_id = await asyncio.to_thread(_tenant_for, aad_tenant_id)
    params = verify(raw, ASK_NUBI, tenant_id) if raw else None
    if not (params and params.get("event_id") and params.get("account_id")):
        if raw and expired(raw, ASK_NUBI, tenant_id):
            return await reply(ASK_NUBI_CARD_TOO_OLD)
        LOG.warning("Rejecting a Teams Ask Nubi click with invalid data")
        return await reply(UNABLE_TO_PROCESS_REQUEST)

    # As on Slack, anyone in the workspace can ask: the alert is already
    # visible in this channel.
    user_id, tenants = await asyncio.to_thread(validate_and_get_user_tenants, user_email)
    if not user_id or str(tenant_id) not in {str(t) for t in tenants or []}:
        return await reply(USER_NOT_FOUND_MESSAGE)

    message_id = activity.reply_to_id
    channel_id = channel_of(activity.conversation.id if activity.conversation else "")
    if not (message_id and channel_id):
        LOG.warning("Rejecting a Teams Ask Nubi click that doesn't name its card")
        return await reply(UNABLE_TO_PROCESS_REQUEST)
    event_id = params["event_id"]
    cache = Cache()
    token = uuid.uuid4().hex
    if not cache.claim_teams_analysis(event_id, _analysis_claim_ttl(kickoff=True), token):
        return await reply(STILL_ANALYZING)
    with _analyses_lock:
        if _active_analyses >= settings.slack.event_analysis_max_pollers:
            busy = True
        else:
            busy = False
            _active_analyses += 1
    if busy:
        cache.release_teams_analysis(event_id, token)
        return await reply(ANALYSIS_BUSY)

    started = False
    card_ref = (tenant_id, channel_id, message_id)
    try:
        card = await asyncio.to_thread(_stamp_analysis, card_ref, "Analyzing… results will appear in this thread.")
        if card:
            await _show(activity, card)
        job = {
            "activity": activity,
            "aad_tenant_id": aad_tenant_id,
            "tenant_id": str(tenant_id),
            "user_id": user_id,
            "event_id": event_id,
            "account_id": params["account_id"],
            "card_ref": card_ref,
            "token": token,
            "deadline": time.monotonic() + settings.slack.event_analysis_max_minutes * 60,
        }
        # The analysis can run for many minutes, so it doesn't hold the inbound
        # handler's threadpool worker.
        _start_analysis(job)
        started = True
    finally:
        if not started:
            cache.release_teams_analysis(event_id, token)
            with _analyses_lock:
                _active_analyses -= 1
            await asyncio.to_thread(_clear_analysis_stamp, card_ref)


def _start_analysis(job) -> None:
    threading.Thread(target=_run_analysis, args=(job,), daemon=True, name=f"teams-analysis-{job['event_id']}").start()


def _clear_analysis_stamp(card_ref) -> None:
    """Best effort: don't leave "Analyzing…" on a card nothing is analysing."""
    try:
        _stamp_analysis(card_ref, None)
    except Exception as e:
        LOG.warning("Clearing the Teams analysis line failed: %s", e)


def _run_analysis(job) -> None:
    global _active_analyses
    try:
        asyncio.run(_analyze(job))
    except Exception as e:
        LOG.exception("Teams event analysis for %s failed: %s", job["event_id"], e)
        _clear_analysis_stamp(job["card_ref"])
    finally:
        Cache().release_teams_analysis(job["event_id"], job["token"])
        with _analyses_lock:
            _active_analyses -= 1


def _cut(text: str, event_id) -> str:
    """The text within WRITEUP_LIMIT bytes (Teams limits a message's size, not
    its characters), ending with a link to the rest."""
    data = text.encode()
    if len(data) <= WRITEUP_LIMIT:
        return text
    head = data[:WRITEUP_LIMIT].decode(errors="ignore").rstrip()
    if head.count("```") % 2:
        head += "\n```"  # close a cut code block, so the link isn't inside it
    url = f"{settings.base_url}/investigate?id={event_id}"
    return f"{head}\n\n… [See the full analysis in NudgeBee]({url})"


async def _take_result(result, progress, post, stamp) -> bool:
    """Post what a poll result newly completed. True once the analysis ended."""
    from notifications_server.services.actions_common import final_writeup

    sent = progress["sent"]
    statuses = result.get("task_statuses") or {}
    status = (result.get("status") or "").upper()
    if progress["first"] and status == "COMPLETED":
        # Finished earlier: its sections are already in the thread.
        sent.update(field for field, _ in ANALYSIS_SECTIONS)
    progress["first"] = False
    for field, preamble in ANALYSIS_SECTIONS:
        if field not in sent and statuses.get(field) == "COMPLETED":
            if await post(result.get(field), preamble):
                sent.add(field)
    if statuses.get("detailed_response") == "COMPLETED":
        content, preamble = final_writeup(
            result.get("detailed_response"), result.get("investigation"), result.get("summary")
        )
        if await post(content, preamble):
            await stamp("Analyzed by Nubi. See the thread.")
            return True
    elif status == "FAILED":
        await post(ANALYSIS_FAILED)
        await stamp(None)
        return True
    return False


async def _analyze(job) -> None:
    """Poll the event analysis, post each section to the card's thread as it
    completes, then mark the card. regenerate is False, so this only reads
    llm-server's state; a repeat after it finished re-posts just the write-up,
    as on Slack."""
    from notifications_server import slack_app, teams_app
    from notifications_server.services.common import CommonService
    from notifications_server.services.events import Events

    activity, event_id = job["activity"], job["event_id"]
    service = CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)
    cache = Cache()

    async def post(content, preamble=""):
        content = (content or "").strip()
        if not content:
            return True
        text = _cut(f"{preamble}\n\n{content}" if preamble else content, event_id)
        return await service.teams_reply(activity, text, job["aad_tenant_id"])

    async def stamp(text):
        card = await asyncio.to_thread(_stamp_analysis, job["card_ref"], text)
        if card:
            await _show(activity, card)

    try:
        progress = {"sent": set(), "first": True}
        failures, calls = 0, 0
        token = job["token"]
        while time.monotonic() < job["deadline"]:
            kickoff = calls == 0
            cache.refresh_teams_analysis(event_id, _analysis_claim_ttl(kickoff=kickoff), token)
            timeout = (
                ANALYSIS_KICKOFF_TIMEOUT_SECONDS if kickoff else settings.slack.event_analysis_poll_timeout_seconds
            )
            calls += 1
            try:
                result = await asyncio.to_thread(
                    Events.call_event_analysis_api,
                    event_id,
                    job["account_id"],
                    job["user_id"],
                    job["tenant_id"],
                    timeout,
                )
            except Exception as e:
                LOG.debug("Teams event analysis poll failed: %s", e)
                result = None
            if not isinstance(result, dict):
                # A slow start isn't a failure: the next poll finds the analysis running.
                if not kickoff:
                    failures += 1
                if failures >= ANALYSIS_MAX_POLL_FAILURES:
                    break
            else:
                failures = 0
                if await _take_result(result, progress, post, stamp):
                    return
            cache.refresh_teams_analysis(event_id, _analysis_claim_ttl(), token)
            await asyncio.sleep(settings.slack.event_analysis_poll_seconds)
        await stamp("Still analyzing. Check the event in NudgeBee.")
    finally:
        service.close()


# ------------------------------- feedback -------------------------------


def _post_feedback(params, tenant_id, user_id) -> bool:
    """Record the rating in api-server, as Slack's feedback does. False when it
    wasn't recorded."""
    from notifications_server.repositories.user_repository import get_llm_conversation_by_session

    with Session(sync_engine) as session:
        conversation = get_llm_conversation_by_session(session, params["session_id"])
    if not conversation:
        LOG.warning("No Nubi conversation for a Teams feedback click on session %s", params["session_id"])
        return False
    useful = bool(params["useful"])
    request = {
        "session_id": params["answer_id"],
        "module": "new-investigation",
        "question": "",
        "llm_response": "",
        "user_corrected_response": "",
        "additional_notes": "User liked the Response" if useful else "User disliked the Response",
        "conversation_id": conversation["id"],
        "cloud_account_id": params["account_id"],
        "useful": useful,
    }
    try:
        response = requests.post(
            settings.services.api_server + "/rpc/ai",
            json={
                "action": {"name": "ai_feedback_create"},
                "input": {"request": request},
                "session_variables": {"tenant_id": str(tenant_id), "user_id": str(user_id)},
            },
            headers={"X-ACTION-TOKEN": settings.action_api_server_token},
            timeout=10,
        )
    except requests.RequestException as e:
        LOG.warning("Recording Teams feedback failed: %s", e)
        return False
    if not response.ok:
        LOG.warning("api-server refused Teams feedback: %s", response.status_code)
    return response.ok


async def _feedback(common_service, activity, user_email, aad_tenant_id) -> None:
    # Imported here: actions_common imports events, which imports this module.
    from notifications_server.services.actions import validate_and_get_user_tenants
    from notifications_server.services.actions_common import UNABLE_TO_PROCESS_REQUEST, USER_NOT_FOUND_MESSAGE

    async def reply(text):
        await common_service.teams_reply(activity, text, aad_tenant_id)

    raw = activity.value.get(FEEDBACK_DATA)
    tenant_id = await asyncio.to_thread(_tenant_for, aad_tenant_id)
    params = verify(raw, FEEDBACK, tenant_id) if raw else None
    if not (params and params.get("answer_id") and params.get("account_id") and params.get("session_id")):
        if raw and expired(raw, FEEDBACK, tenant_id):
            return await reply(FEEDBACK_CARD_TOO_OLD)
        LOG.warning("Rejecting a Teams feedback click with invalid data")
        return await reply(UNABLE_TO_PROCESS_REQUEST)

    user_id, tenants = await asyncio.to_thread(validate_and_get_user_tenants, user_email)
    if not user_id or str(tenant_id) not in {str(t) for t in tenants or []}:
        return await reply(USER_NOT_FOUND_MESSAGE)

    message_id = activity.reply_to_id
    channel_id = channel_of(activity.conversation.id if activity.conversation else "")
    if not (message_id and channel_id):
        LOG.warning("Rejecting a Teams feedback click that doesn't name its card")
        return await reply(UNABLE_TO_PROCESS_REQUEST)
    card_ref = (tenant_id, channel_id, message_id)
    claim = f"{tenant_id}:{message_id}:{FEEDBACK}"
    cache = Cache()
    if not cache.claim_card_action(claim, CLAIM_SECONDS):
        return await reply(FEEDBACK_ALREADY)
    try:
        started = await asyncio.to_thread(begin, card_ref, FEEDBACK, lambda c: c)
        if started is REFUSED or started is BUSY:
            return await reply(FEEDBACK_ALREADY)
        if not await asyncio.to_thread(_post_feedback, params, tenant_id, user_id):
            await asyncio.to_thread(finish, card_ref, FEEDBACK, None, lambda c: c)
            return await reply(FEEDBACK_FAILED)
        clicker = (activity.from_property.name if activity.from_property else None) or user_email
        outcome = f"{'👍' if params['useful'] else '👎'} Rated by {clicker}. Thanks for the feedback."
        card = await asyncio.to_thread(
            finish, card_ref, FEEDBACK, DONE, lambda c: with_status(without_feedback(c), outcome)
        )
        if card:
            await _show(activity, card)
        else:
            await reply(outcome)
    except Exception as e:
        LOG.exception("Recording Teams feedback failed: %s", e)
        await reply(FEEDBACK_FAILED)
    finally:
        cache.release_card_action(claim)
