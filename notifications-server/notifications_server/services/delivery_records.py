"""Delivery history in sent_notifications: one row per attempt to send one
message to one destination.

Every message has one writer. The send path writes the rows threading, Teams
buttons and remediation act on (first posts of an alert, Teams cards with
buttons, remediation markers and waiting posts), as before, and fill() adds
their delivery columns. Everything else is recorded here, best effort: nothing
reads these rows to decide what to send, so losing one never causes a
duplicate post.

Rows the send path wrote before the delivery columns existed have no kind;
the lookups below still find them through the platform-named columns."""

import json
import logging
import uuid
from dataclasses import dataclass, field
from datetime import timedelta
from types import SimpleNamespace
from typing import Any, Dict, Iterable, List, Optional

from sqlalchemy import and_, or_
from sqlalchemy.orm import Session

from notifications_server.configs import settings
from notifications_server.models.db_base import BaseDB
from notifications_server.models.models import SentNotifications
from notifications_server.services.delivery_errors import (
    INTERNAL_ERROR,
    WEBHOOK_PLATFORMS,
    delivery_channel_id,
    email_reason,
)
from notifications_server.services.rules import AUTOMATION_SOURCE
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

FINDING = "finding"
FINDING_RESOLVED = "finding_resolved"
APPROVAL = "approval"
AUTOMATION = "automation"
REMEDIATION = "remediation"
REMEDIATION_MARKER = "remediation_marker"
REMEDIATION_PENDING = "remediation_pending"
NUBI_ANSWER = "nubi_answer"
EMAIL_PREFIX = "email:"
# The kinds whose Teams rows hold a card that clicks update.
TEAMS_CARD_KINDS = (FINDING, APPROVAL, NUBI_ANSWER)

SENT = "sent"
FAILED = "failed"
NOT_SENT = "not_sent"
PENDING = "pending"

# Why a message was not sent, besides NO_DESTINATION.
SUPPRESSED = "suppressed"
SNOOZED = "snoozed"
HELD_FOR_SUMMARY = "held_for_summary"
DIGEST_ONLY = "digest_only"
DROPPED_BY_DEFAULT = "dropped_by_default"

# A repeat of an alert replies in its thread only this long after the first post.
REPEAT_WINDOW = timedelta(hours=24)
# How far back a resolved reply looks for the alert's thread: the rows' retention.
RESOLVED_WINDOW = timedelta(days=7)

TITLE_LIMIT = 200

# Set on a result whose row the send path wrote; taken off before results
# leave the send, so callers never see it.
SEND_PATH_ROW = "_send_path_row"

_SUMMARY_NAMES = {"grouped_anomaly": "Anomaly summary", "grouped_slo_alert": "SLO summary"}
_TEAMS_CARD_KIND = {"answer": NUBI_ANSWER}


@dataclass
class Dispatch:
    """One send request: what is sent, and what each of its rows shares."""

    tenant_id: Optional[uuid.UUID]
    kind: Optional[str]
    dispatch_id: uuid.UUID = field(default_factory=uuid.uuid4)
    attempt: int = 1
    source: Optional[str] = None
    title: Optional[str] = None
    event_id: Optional[uuid.UUID] = None
    fingerprint: Optional[str] = None
    account_id: Optional[uuid.UUID] = None
    # The rule entries that route the message, and every entry that matched it.
    targets: List[Dict[str, Any]] = field(default_factory=list)
    matched: List[Dict[str, Any]] = field(default_factory=list)
    # The caller named the channels.
    requested: bool = False
    stopped_by: Any = None


def as_uuid(value) -> Optional[uuid.UUID]:
    if isinstance(value, uuid.UUID):
        return value
    try:
        return uuid.UUID(str(value)) if value else None
    except (TypeError, ValueError, AttributeError):
        return None


def kind_of(type_, parameters, source=None) -> str:
    if type_ in (FINDING, FINDING_RESOLVED):
        return type_
    if type_ in (None, "", "generic"):
        if (parameters or {}).get("approval_token"):
            return APPROVAL
        if source == AUTOMATION_SOURCE:
            return AUTOMATION
        return "generic"
    return type_


def _first_line(text) -> Optional[str]:
    if not isinstance(text, str):
        return None
    return next((line.strip() for line in text.splitlines() if line.strip()), None)


def _cap(text) -> Optional[str]:
    if not text:
        return None
    text = str(text)
    return text if len(text) <= TITLE_LIMIT else text[: TITLE_LIMIT - 1] + "…"


def title_of(kind, parameters) -> Optional[str]:
    """What was sent, in a line: the alert's title, a summary's name and alert
    count, an approval's automation, an automation post's first line. Never an
    approval token or a message body."""
    try:
        params = parameters or {}
        if kind in (FINDING, FINDING_RESOLVED):
            finding = params.get("finding") or {}
            return _cap(finding.get("title") or finding.get("aggregation_key"))
        if kind == APPROVAL:
            workflow = params.get("workflow_name")
            return _cap(f"Approval for {workflow}" if workflow else "Approval request")
        if kind in (AUTOMATION, "generic"):
            return _cap(_first_line(params.get("message")))
        if kind in _SUMMARY_NAMES:
            return _cap(f"{_SUMMARY_NAMES[kind]} ({len(params.get('events') or [])} alerts)")
        return _cap(str(kind).replace("_", " ").capitalize())
    except Exception:
        return None


def _event_id_of(type_, parameters) -> Optional[uuid.UUID]:
    params = parameters or {}
    if type_ in (FINDING, FINDING_RESOLVED):
        finding = params.get("finding") or {}
        return as_uuid(finding.get("id") or finding.get("event_id"))
    metadata = params.get("workflow_metadata") or {}
    return as_uuid(params.get("event_id") or (metadata.get("event_id") if isinstance(metadata, dict) else None))


def _account_of(type_, parameters) -> Optional[uuid.UUID]:
    params = parameters or {}
    if type_ in (FINDING, FINDING_RESOLVED):
        return as_uuid((params.get("finding") or {}).get("cloud_account_id"))
    metadata = params.get("workflow_metadata") or {}
    return as_uuid(params.get("account_id") or (metadata.get("account_id") if isinstance(metadata, dict) else None))


def dispatch_for(tenant_id, type_, payload, source=None, dispatch_id=None, attempt=None) -> Dispatch:
    """The Dispatch of a send request. source is the rule category the message
    falls under. A queued message carries its id and attempt; an HTTP request
    or a summary flush gets a new id."""
    try:
        parameters = (payload or {}).get("parameters") or {}
        kind = kind_of(type_, parameters, (payload or {}).get("source"))
        fingerprint = (
            (parameters.get("finding") or {}).get("fingerprint")
            if type_ in (FINDING, FINDING_RESOLVED)
            else parameters.get("fingerprint")
        )
        return Dispatch(
            tenant_id=as_uuid(tenant_id),
            kind=kind,
            dispatch_id=as_uuid(dispatch_id) or uuid.uuid4(),
            attempt=int(attempt or 1),
            source=source or None,
            title=title_of(kind, parameters),
            event_id=_event_id_of(type_, parameters),
            fingerprint=fingerprint or None,
            account_id=_account_of(type_, parameters),
        )
    except Exception:
        LOG.warning("Building the delivery record of a %s message failed", type_, exc_info=True)
        return Dispatch(tenant_id=as_uuid(tenant_id), kind=type_ or None)


def routed(dispatch: Dispatch, outcome, payload) -> None:
    """Note on dispatch which rules decided where the message goes."""
    dispatch.targets = list(getattr(outcome, "targets", None) or [])
    dispatch.stopped_by = getattr(outcome, "stopped_by", None)
    dispatch.requested = bool((payload or {}).get("channels"))


def _routes_to(target, platform, destination) -> bool:
    channels = target.get("channels")
    if platform in WEBHOOK_PLATFORMS:
        return isinstance(channels, dict) and str(channels.get("webhook_target_id")) == str(destination)
    return delivery_channel_id(SimpleNamespace(platform=platform, id=None), channels) == destination


def routed_by(dispatch: Optional[Dispatch], platform, destination):
    """The rules that sent the message to this destination, or "requested" when
    the caller named the channel, or "default" for the installation's own."""
    if dispatch is None or not platform:
        return None
    if dispatch.requested:
        return "requested"
    rules = []
    for target in dispatch.targets:
        if target.get("rule_id") and target.get("platform") == platform and _routes_to(target, platform, destination):
            entry = {"id": str(target["rule_id"]), "name": target.get("name")}
            if entry not in rules:
                rules.append(entry)
    return rules or "default"


def _stopping_rule(matched, stopped_by):
    if not stopped_by:
        return None
    name = next((m.get("name") for m in matched or () if str(m.get("rule_id")) == str(stopped_by)), None)
    return [{"id": str(stopped_by), "name": name}]


def _not_sent_reason(status, stopped_by) -> Optional[str]:
    if status == "suppressed":
        return SUPPRESSED if stopped_by else DROPPED_BY_DEFAULT
    return _DECIDED.get(status)


def stopped(outcome, matched) -> Dict[str, Any]:
    """Why rules stopped a message sent without a Dispatch, such as an email,
    and the rule that stopped it."""
    responses = getattr(outcome, "response", None) or []
    response = responses[0] if isinstance(responses, list) and responses else responses
    status = response.get("status") if isinstance(response, dict) else None
    stopped_by = getattr(outcome, "stopped_by", None)
    return {
        "reason": _not_sent_reason(status, stopped_by) or SUPPRESSED,
        "routed_by": _stopping_rule(matched, stopped_by),
    }


def _thread_of(result) -> Optional[str]:
    return result.get("thread_id") or result.get("thread_ts") or result.get("parent_message_id") or None


_DECIDED = {"snoozed": SNOOZED, "queued": HELD_FOR_SUMMARY, "severity_gated": DIGEST_ONLY}


def new_row(dispatch: Dispatch, status, **columns) -> SentNotifications:
    """A delivery row of dispatch; columns override what the dispatch gives."""
    return SentNotifications(
        id=uuid.uuid4(),
        created_at=utc_now(),
        tenant_id=dispatch.tenant_id,
        account_id=dispatch.account_id,
        fingerprint=dispatch.fingerprint,
        dispatch_id=dispatch.dispatch_id,
        attempt=dispatch.attempt,
        kind=columns.pop("kind", None) or dispatch.kind,
        source=dispatch.source,
        title=columns.pop("title", None) or dispatch.title,
        event_id=dispatch.event_id,
        status=status,
        **columns,
    )


def _row_for(dispatch: Dispatch, result) -> Optional[SentNotifications]:
    status = result.get("status")
    platform = result.get("platform")
    platform = None if platform in (None, "", "system", "unknown") else platform
    reason = result.get("reason")
    error_code = result.get("error_code")
    if status == "success":
        row_status, reason = SENT, None
    elif status in ("failed", NOT_SENT):
        row_status = FAILED if status == "failed" else NOT_SENT
    elif status == "suppressed" or status in _DECIDED:
        row_status, reason = NOT_SENT, _not_sent_reason(status, dispatch.stopped_by)
    elif status == "ignored":
        # The grouped digest could not take the message (no Redis), so it was dropped.
        row_status, reason, error_code = FAILED, INTERNAL_ERROR, "grouping_unavailable"
    else:
        return None  # an internal no-op, such as a resolved reply already sent
    destination = result.get("channel_id")
    if row_status == NOT_SENT:
        routing = _stopping_rule(dispatch.matched, dispatch.stopped_by)
    elif dispatch.kind == FINDING_RESOLVED:
        routing = result.get("routed_by")  # a resolved reply goes where its alert went, routed as the alert was
    else:
        routing = routed_by(dispatch, platform, destination)
    return new_row(
        dispatch,
        row_status,
        platform=platform,
        team_id=result.get("team_id"),
        destination=str(destination) if destination else None,
        destination_name=result.get("destination_name"),
        message_id=result.get("message_ts"),
        thread_id=_thread_of(result),
        reason=reason,
        error_code=error_code,
        routed_by=routing,
    )


def take_marks(results) -> None:
    """Take the send-path marks off results before they leave the send."""
    for result in results if isinstance(results, list) else [results]:
        if isinstance(result, dict):
            result.pop(SEND_PATH_ROW, None)


def rows_for(dispatch: Optional[Dispatch], results) -> List[SentNotifications]:
    """The rows to record for a send's results: every result except those whose
    row the send path wrote. Takes the marks off as it goes."""
    if isinstance(results, dict):
        results = [results] if results.get("status") in ("queued", "ignored") else []
    rows = []
    for result in results or []:
        if not isinstance(result, dict) or result.pop(SEND_PATH_ROW, False):
            continue
        if dispatch is None or dispatch.tenant_id is None:
            continue
        try:
            row = _row_for(dispatch, result)
        except Exception:
            LOG.warning("Building a delivery row failed", exc_info=True)
            continue
        if row is not None:
            rows.append(row)
    return rows


def email_rows(
    tenant_id,
    template,
    subject,
    outcomes: Dict[str, Any],
    not_sent_reason=None,
    dispatch_id=None,
    attempt=1,
    source=None,
    routed_by=None,
) -> List[SentNotifications]:
    """One row per recipient of an email: outcomes maps each to the error its
    send raised, or None when it was sent. With not_sent_reason, one not_sent
    row for the email instead, naming the rule that stopped it in routed_by."""
    try:
        dispatch = Dispatch(
            tenant_id=as_uuid(tenant_id),
            kind=f"{EMAIL_PREFIX}{template or 'message'}",
            dispatch_id=as_uuid(dispatch_id) or uuid.uuid4(),
            attempt=int(attempt or 1),
            source=source or None,
            title=_cap(subject),
        )
        if dispatch.tenant_id is None:
            return []
        if not_sent_reason:
            return [new_row(dispatch, NOT_SENT, platform="email", reason=not_sent_reason, routed_by=routed_by)]
        rows = []
        for recipient, error in outcomes.items():
            if error is None:
                rows.append(new_row(dispatch, SENT, platform="email", destination=recipient))
            else:
                reason, code = email_reason(error)
                rows.append(
                    new_row(dispatch, FAILED, platform="email", destination=recipient, reason=reason, error_code=code)
                )
        return rows
    except Exception:
        LOG.warning("Building the delivery rows of an email failed", exc_info=True)
        return []


def fill(row: SentNotifications, dispatch: Optional[Dispatch] = None, **columns) -> SentNotifications:
    """Fill the delivery columns of a row the send path wrote, from the
    platform-named columns it already has. Never raises: a bug here must not
    fail the write threading depends on."""
    try:
        if row.slack_thread_id:
            platform, team, message_id = "slack", row.slack_team_id, row.slack_thread_id
            destination = (json.loads(row.slack_metadata or "{}") or {}).get("channel")
        elif row.discord_message_id:
            platform, team, message_id, destination = "discord", None, row.discord_message_id, row.discord_channel_id
        elif row.teams_message_id:
            platform, team, message_id, destination = "ms_teams", None, row.teams_message_id, row.teams_channel_id
        else:
            return row
        card_kind = (json.loads(row.teams_metadata or "{}") or {}).get("kind") if row.teams_metadata else None
        row.platform = platform
        row.team_id = columns.get("team_id") or team
        row.destination = destination
        row.destination_name = columns.get("destination_name") or row.destination_name
        row.message_id = message_id
        row.thread_id = columns.get("thread_id") or row.thread_id
        row.status = SENT
        row.kind = row.kind or _TEAMS_CARD_KIND.get(card_kind, card_kind) or (dispatch.kind if dispatch else FINDING)
        if dispatch is not None:
            row.dispatch_id = dispatch.dispatch_id
            row.attempt = dispatch.attempt
            row.source = dispatch.source
            row.title = dispatch.title
            row.event_id = dispatch.event_id
            row.routed_by = routed_by(dispatch, platform, destination)
    except Exception:
        LOG.warning("Filling the delivery columns of a sent row failed", exc_info=True)
    return row


def fill_all(rows: Iterable[SentNotifications], dispatch: Optional[Dispatch] = None, results=()) -> None:
    """fill() each row, with the destination name, team and thread from the
    send's result for it."""
    by_message = {
        (result.get("platform"), result.get("message_ts")): result
        for result in results or ()
        if isinstance(result, dict) and result.get("message_ts")
    }
    for row in rows:
        fill(row, dispatch)
        result = by_message.get((row.platform, row.message_id)) or {}
        row.destination_name = row.destination_name or result.get("destination_name")
        row.team_id = row.team_id or result.get("team_id")
        row.thread_id = row.thread_id or _thread_of(result)


def _recording() -> bool:
    return settings.notifications.delivery_history_recording_enabled


async def record(engine, rows: List[SentNotifications]) -> None:
    """Insert rows in their own session. Never raises and never retries: the
    send already happened."""
    if not rows or not _recording():
        return
    try:
        async with BaseDB.async_session(engine)() as session:
            session.add_all(rows)
            await session.commit()
    except Exception as exc:
        LOG.warning("Recording %d delivery rows failed: %s", len(rows), type(exc).__name__)


def record_sync(rows: List[SentNotifications], engine=None) -> None:
    """record() for code that runs outside the event loop."""
    if not rows or not _recording():
        return
    if engine is None:
        from notifications_server import sync_engine as engine

    try:
        with Session(engine) as session:
            session.add_all(rows)
            session.commit()
    except Exception as exc:
        LOG.warning("Recording %d delivery rows failed: %s", len(rows), type(exc).__name__)


async def record_results(engine, dispatch: Optional[Dispatch], results) -> None:
    """Record a send's results, and take the send-path marks off them."""
    rows = rows_for(dispatch, results)
    take_marks(results)
    await record(engine, rows)


# ----------------------------- Lookups -----------------------------
# Each matches rows written with the delivery columns, or by a release that
# predates them (no kind) through the platform-named columns.


def written_before_delivery_columns():
    return SentNotifications.kind.is_(None)


def first_post(platform):
    """A sent first post of an alert on platform. The send path writes these
    only for an alert with a fingerprint; a recorded post without one is never
    an alert's thread."""
    return and_(
        SentNotifications.kind == FINDING,
        SentNotifications.platform == platform,
        SentNotifications.fingerprint.isnot(None),
        SentNotifications.status == SENT,
        SentNotifications.message_id.isnot(None),
        SentNotifications.thread_id.is_(None),
    )


def slack_first_post():
    """A Slack first post of an alert, written either way."""
    return or_(
        first_post("slack"),
        and_(written_before_delivery_columns(), SentNotifications.slack_thread_id.isnot(None)),
    )


def teams_card(channel_id, message_id):
    """The row holding the Teams card a channel and message id name."""
    return and_(
        SentNotifications.teams_metadata.isnot(None),
        or_(
            and_(
                SentNotifications.platform == "ms_teams",
                SentNotifications.kind.in_(TEAMS_CARD_KINDS),
                SentNotifications.destination == channel_id,
                SentNotifications.message_id == message_id,
            ),
            and_(
                written_before_delivery_columns(),
                SentNotifications.teams_channel_id == channel_id,
                SentNotifications.teams_message_id == message_id,
            ),
        ),
    )


def of_kind(kind):
    """Rows of kind, or rows that predate the kind column."""
    return or_(SentNotifications.kind == kind, written_before_delivery_columns())
