"""The remediation loop in the alert's Slack thread (#40734).

llm-server draws the thread from its own state (syncAlertThread): one fix-options message per plan version, edited
in place as the state moves, a verdict message per likely-fixed run, and one-off reports. Each message has a key;
its marker row in sent_notifications (``remediation-msg:<thread fingerprint>:<key>``) keeps the posted ts, the
content hash and the state_seq that drew it. A send is posted when its key is new and edited when the content
changed; one drawn from an older state (a lower state_seq) than the thread shows is refused, and while another send
for the same thread is being posted the answer is "busy", which llm-server retries. Buttons carry nothing but what
was clicked: the message metadata names the plan or run, and every click is decided by the server as the clicker's
Identity Sync user (remediation_actions.py).

Before that, llm-server posted a header and one message per step with Run and Decline (a ``remediation:`` token).
Those messages may still be in threads: with the loop on their buttons are retired; with it off a click is decided
as before. Posts for an alert with no Slack thread yet wait, stored, and go in when the thread is created."""

import hashlib

import json
import logging
import time
import uuid
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from typing import List, Optional, Tuple

import requests
from pydantic import BaseModel
from slack_sdk.errors import SlackApiError
from sqlalchemy import text
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from notifications_server import slack_app, sync_engine
from notifications_server.configs.settings import settings
from notifications_server.message_templates.slack.generic import _build_buttons
from notifications_server.models.models import SentNotifications
from notifications_server.services import delivery_records
from notifications_server.services.cache import Cache
from notifications_server.services.common import latest_slack_notification
from notifications_server.services.delivery_errors import slack_failure
from notifications_server.services.messaging_installations import load_installation
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.feature_flag_utils import is_feature_enabled
from notifications_server.utils.transformer import Transformer

LOG = logging.getLogger(__name__)

TOKEN_PREFIX = "remediation:"
# sent_notifications fingerprints: a send already posted (by its key), and posts waiting for the alert's thread.
POSTED_PREFIX = "remediation-posted:"
PENDING_PREFIX = "remediation-pending:"
EXECUTE_PATH = "/v1/analyze/remediation/execute"
SLOTS = {"run": "execute", "decline": "decline"}
# Slack's limit on a section's text, after escaping.
SLACK_TEXT_LIMIT = 2900
# How long a click waits for its run: past llm-server's RunSendTimeout (10 minutes).
RUN_WAIT_SECONDS = 660
# Slack clicks wait on their run in this pool, never in the server's request threads (a run can take minutes).
RUNS = ThreadPoolExecutor(max_workers=16, thread_name_prefix="remediation-run")
# How long one send holds its claim, and the longest rate-limit wait honoured before a post is given up.
CLAIM_SECONDS = 60
RATE_LIMIT_WAIT = 10
# A send that waits longer than this for the alert's thread is dropped, not posted.
PENDING_LIFETIME = timedelta(hours=24)

NOT_LINKED = (
    "Your Slack account isn't linked to a {brand} user in this workspace, so nothing was recorded."
    " Ask a {brand} admin to link it in Identity Sync, then press the button again."
)
UNREACHABLE = "Couldn't reach {brand} to record that. Please try again."
STILL_RUNNING = "It's still running. Its outcome will show on the alert; don't run it again until it does."
CANT_READ = "I couldn't read that button, so nothing was recorded."


# A keyed message's marker, an episode's pointer to its main alert, and the one waiting state send of a thread.
MESSAGE_PREFIX = "remediation-msg:"
EPISODE_PREFIX = "remediation-episode:"
STATE_PENDING = "state"
# Slack's answers to an edit of a message that is gone or no longer the app's to edit: it is posted again.
REPOST_ERRORS = {"message_not_found", "cant_update_message", "edit_window_closed"}
# Message tokens: the fix options of one plan version, and one run's verdict.
PLAN_TOKEN_PREFIX = "remediation-plan:"
RUN_TOKEN_PREFIX = "remediation-run:"
# Slack allows 25 elements in an actions block, and 50 blocks in a message: the text, the sections, the footer and the
# buttons below.
MAX_BUTTONS = 25
MAX_SECTIONS = 46


class Button(BaseModel):
    """A button llm-server asks for. kind: pick (Use this fix), pr (Raise a PR), manual (I made this change), other
    (Other fixes…), resolve, or link (url)."""

    kind: str
    label: str
    option: Optional[int] = None
    run_id: Optional[str] = None
    style: Optional[str] = None
    url: Optional[str] = None


class Section(BaseModel):
    """A part of a message drawn after its text, as its own Slack section, with its button beside it (one fix
    option)."""

    text: str
    button: Optional[Button] = None


class Post(BaseModel):
    text: str
    token: Optional[str] = None
    # Names this message in the alert's thread: posted once, and with edit, edited in place by a later send.
    key: Optional[str] = None
    edit: bool = False
    # Only edited, never posted: a message that was replaced (the older plan's options).
    only_if_posted: bool = False
    # The message's buttons (a keyed message's; legacy step messages use token alone) and its status footer.
    buttons: Optional[List[Button]] = None
    status: Optional[str] = None
    # Parts drawn after the text, each with the button beside it (the fix options).
    sections: Optional[List[Section]] = None


class Posts(BaseModel):
    """One send from llm-server: messages for one alert's thread, in order."""

    tenant_id: str
    account_id: str
    event_id: str
    fingerprint: str = ""
    # Names the send so it is posted once (a plan's version); empty for a note. Sends with keyed posts don't use it.
    key: Optional[str] = None
    posts: List[Post]
    # When the send first had to wait for the alert's thread (epoch seconds),
    # kept while it waits again.
    queued_at: Optional[float] = None
    # The alert state the send was drawn from, numbered in order (llm-server's remediation_thread_state_seq): a send
    # older than the one the thread shows is refused.
    state_seq: Optional[int] = None
    # The episode's main alert, when this thread is a duplicate's (DUPLICATE or SUPPRESSED follower).
    main_fingerprint: Optional[str] = None


# The tenant feature flag that turns the remediation loop on, for every account of the tenant: llm-server's
# events.RemediationLoopFeature.
FEATURE = "EVENT_REMEDIATION_LOOP"


def loop_on(session, tenant_id) -> bool:
    """Whether tenant_id has the remediation loop on (its tenant-wide feature_flag row)."""
    return bool(tenant_id) and is_feature_enabled(session, FEATURE, str(tenant_id))


def tenant_loop_on(tenant_id) -> bool:
    """loop_on on its own session; a flag that can't be read counts as on (the server refuses a Run then anyway)."""
    try:
        with Session(sync_engine) as session:
            return loop_on(session, tenant_id)
    except Exception:
        LOG.warning("remediation: the loop flag of %s was not read", tenant_id, exc_info=True)
        return True


def is_token(token) -> bool:
    return isinstance(token, str) and token.startswith(TOKEN_PREFIX)


def parse_token(token) -> Optional[Tuple[str, str, str, str, int]]:
    """(tenant, account, event, plan version, step), or None when the token isn't one."""
    if not is_token(token):
        return None
    parts = token[len(TOKEN_PREFIX) :].split(":")
    if len(parts) != 5:
        return None
    try:
        tenant, account, event = (str(uuid.UUID(p)) for p in parts[:3])
        step = int(parts[4])
    except ValueError:
        return None
    if not parts[3] or step < 0:
        return None
    return tenant, account, event, parts[3], step


def _uuid(value):
    return uuid.UUID(str(value)) if value else None


def _cap(value, limit=SLACK_TEXT_LIMIT) -> str:
    value = str(value or "")
    return value if len(value) <= limit else value[: limit - 1] + "…"


def slack_text(post: Post) -> str:
    """A post's markdown as Slack mrkdwn, converted as an investigation's answer is."""
    return Transformer.markdown_to_slack_markdown(post.text)


def button_value(button: Button) -> str:
    """What a button carries back: what was clicked, never a credential (the message metadata names the plan)."""
    value = {"k": button.kind}
    if button.option is not None:
        value["o"] = button.option
    if button.run_id:
        value["r"] = button.run_id
    return json.dumps(value, separators=(",", ":"))


def _button(button: Button, index: int) -> dict:
    out = {
        "type": "button",
        "text": {"type": "plain_text", "text": _cap(button.label, 75)},
        "action_id": f"remediation_{button.kind}_{index}",
    }
    if button.url:
        out["url"] = button.url
    else:
        out["value"] = button_value(button)
    if button.style in ("primary", "danger"):
        out["style"] = button.style
    return out


def blocks(post: Post) -> list:
    """A post as Slack blocks: its text, its sections (each with its button beside it), its status footer, and its
    buttons. A legacy step message (a ``remediation:`` token and no buttons) gets main's approval buttons (Run,
    Decline); the token is in the message metadata, as main's approval messages keep it."""
    out = [{"type": "section", "text": {"type": "mrkdwn", "text": _cap(slack_text(post))}}]
    sections = (post.sections or [])[:MAX_SECTIONS]
    for i, section in enumerate(sections):
        block = {
            "type": "section",
            "text": {"type": "mrkdwn", "text": _cap(Transformer.markdown_to_slack_markdown(section.text))},
        }
        if section.button:
            block["accessory"] = _button(section.button, i)
        out.append(block)
    if post.status:
        out.append(
            {
                "type": "context",
                "elements": [{"type": "mrkdwn", "text": _cap(Transformer.markdown_to_slack_markdown(post.status))}],
            }
        )
    if post.buttons is not None:
        if post.buttons:
            out.append(
                {
                    "type": "actions",
                    "elements": [_button(b, len(sections) + i) for i, b in enumerate(post.buttons[:MAX_BUTTONS])],
                }
            )
        return out
    if is_token(post.token):
        run, decline = _build_buttons(["run", "decline"])
        run["style"] = "primary"
        run["confirm"] = {
            "title": {"type": "plain_text", "text": "Run this step?"},
            "text": {"type": "mrkdwn", "text": "It runs the command above on this alert's account, as you."},
            "confirm": {"type": "plain_text", "text": "Run"},
            "deny": {"type": "plain_text", "text": "Cancel"},
        }
        out.append({"type": "actions", "elements": [run, decline]})
    return out


def deliver(req: Posts) -> str:
    """Post req in the alert's thread: "posted", "already" (its key is posted or being posted), "pending" (no
    thread yet), or "failed" (Slack took none of its posts). A send of keyed messages may also be "busy" (another
    send for the thread is being posted: retry it) or "stale" (the thread shows a newer state)."""
    if any(p.key for p in req.posts):
        return deliver_keyed(req)
    claim = f"remediation-thread:{req.tenant_id}:{req.key}" if req.key else None
    cache = Cache()
    token = cache.claim_owned(claim, CLAIM_SECONDS) if claim else ""
    if token is None:
        return "already"  # the same send is being posted; a retry after llm-server's timeout must not repost
    try:
        return _deliver(req)
    finally:
        if claim:
            cache.release_owned(claim, token)


def _deliver(req: Posts) -> str:
    tenant = _uuid(req.tenant_id)
    marker_id = None
    # Slack is called, and the posts recorded, after this session ends: an open transaction here would hold
    # back the recorder's own connection, and with it every write to the table queued behind a lock request.
    with Session(sync_engine) as session:
        if req.key:
            if (
                session.query(SentNotifications)
                .filter_by(tenant_id=tenant, fingerprint=POSTED_PREFIX + req.key)
                .filter(delivery_records.of_kind(delivery_records.REMEDIATION_MARKER))
                .first()
            ):
                return "already"
        alert = latest_slack_notification(session, req.fingerprint, tenant, _uuid(req.account_id))
        channel = json.loads(alert.slack_metadata or "{}").get("channel") if alert else None
        installation = load_installation(session, req.tenant_id, "slack") if channel else None
        if not installation:
            session.add(
                SentNotifications(
                    id=uuid.uuid4(),
                    created_at=utc_now(),
                    tenant_id=tenant,
                    account_id=_uuid(req.account_id),
                    fingerprint=f"{PENDING_PREFIX}{req.fingerprint}:{uuid.uuid4()}",
                    kind=delivery_records.REMEDIATION_PENDING,
                    platform="slack",
                    status=delivery_records.PENDING,
                    slack_metadata=req.model_copy(update={"queued_at": req.queued_at or time.time()}).model_dump_json(),
                )
            )
            session.commit()
            return "pending"
        # Read before the marker's commit expires them.
        token, thread_ts, team_id = installation.token, alert.slack_thread_id, alert.slack_team_id
        channel_name = alert.destination_name
        if req.key:
            # Marked before posting, so a send that outlives llm-server's wait is not posted again by its retry.
            marker_id = uuid.uuid4()
            session.add(
                SentNotifications(
                    id=marker_id,
                    created_at=utc_now(),
                    tenant_id=tenant,
                    account_id=_uuid(req.account_id),
                    fingerprint=POSTED_PREFIX + req.key,
                    slack_team_id=team_id,
                    kind=delivery_records.REMEDIATION_MARKER,
                    platform="slack",
                    team_id=team_id,
                )
            )
            session.commit()
    sent = delivery_records.Dispatch(
        tenant_id=tenant,
        kind=delivery_records.REMEDIATION,
        account_id=_uuid(req.account_id),
        fingerprint=req.fingerprint or None,
        event_id=delivery_records.as_uuid(req.event_id),
    )
    rows = []
    posted = sum(_post(token, channel, thread_ts, post, req.event_id, sent, team_id, rows) for post in req.posts)
    for row in rows:
        row.destination_name = channel_name  # every post goes in the alert's thread
    delivery_records.record_sync(rows, engine=sync_engine)
    if req.posts and not posted:
        if marker_id is not None:
            with Session(sync_engine) as session:
                session.query(SentNotifications).filter_by(id=marker_id).delete()
                session.commit()
        return "failed"  # nothing went in: not marked posted, so llm-server's retry posts it
    if posted < len(req.posts):
        # Kept marked: a retry would post the others again, every time a later note re-sends the plan.
        LOG.warning("remediation thread for %s: %d of %d posts went in", req.event_id, posted, len(req.posts))
    return "posted"


def _post(token, channel, thread_ts, post: Post, event_id, sent=None, team_id=None, rows=None) -> bool:
    """One post in the thread, retried once after Slack's rate limit asks to wait. Each post stands alone: one
    that fails does not hold back the rest. With sent, the post's delivery row is added to rows."""
    for attempt in range(2):
        try:
            response = slack_app.client.reply_in_thread(
                token=token,
                channel_id=channel,
                thread_ts=thread_ts,
                text=_cap(slack_text(post), 300),
                blocks=blocks(post),
                metadata=(
                    {"event_type": "remediation_step", "event_payload": {"token": post.token}} if post.token else None
                ),
            )
            _record_post(rows, sent, team_id, channel, thread_ts, post, ts=response.get("ts") if response else None)
            return True
        except SlackApiError as e:
            wait = int((e.response.headers or {}).get("Retry-After", 0) or 0) if e.response is not None else 0
            if attempt == 0 and e.response is not None and e.response.status_code == 429 and wait <= RATE_LIMIT_WAIT:
                time.sleep(wait or 1)
                continue
            LOG.warning("remediation thread for %s: a post failed: %s", event_id, e)
            _record_post(rows, sent, team_id, channel, thread_ts, post, failed=True, refusal=e.response)
        except Exception as e:
            LOG.warning("remediation thread for %s: a post failed: %s", event_id, e)
            _record_post(rows, sent, team_id, channel, thread_ts, post, failed=True)
        return False
    return False


def _record_post(rows, sent, team_id, channel, thread_ts, post: Post, ts=None, failed=False, refusal=None) -> None:
    """Add one post's delivery row to rows: sent with its message id, or failed
    with the reason Slack's refusal gives."""
    if sent is None or rows is None:
        return
    try:
        columns = {
            "platform": "slack",
            "team_id": team_id,
            "destination": channel,
            "thread_id": thread_ts,
            "title": delivery_records.title_of(delivery_records.AUTOMATION, {"message": post.text}),
        }
        if not failed:
            row = delivery_records.new_row(sent, delivery_records.SENT, message_id=ts, **columns)
        else:
            refused = slack_failure(refusal)
            row = delivery_records.new_row(
                sent,
                delivery_records.FAILED,
                reason=refused.get("reason"),
                error_code=refused.get("error_code"),
                **columns,
            )
        rows.append(row)
    except Exception:
        LOG.warning("Recording a remediation post failed", exc_info=True)


def message_marker(fingerprint, key) -> str:
    """The marker row's fingerprint for one keyed message in one alert's thread."""
    return f"{MESSAGE_PREFIX}{fingerprint}:{key}"


def _marker_state(row) -> dict:
    try:
        state = json.loads(row.slack_metadata or "{}")
    except (TypeError, ValueError):
        state = {}
    return state if isinstance(state, dict) else {}


def _metadata(post: Post, req: Posts) -> Optional[dict]:
    if not post.token:
        return None
    return {
        "event_type": "remediation_loop",
        "event_payload": {"token": post.token, "main_fingerprint": req.main_fingerprint or req.fingerprint or ""},
    }


def _content_hash(post: Post, req: Posts) -> str:
    body = json.dumps([blocks(post), _metadata(post, req)], sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(body.encode()).hexdigest()


def thread_claim(tenant_id, fingerprint) -> str:
    """One send at a time per alert thread, so edits land in the order of their state."""
    return f"remediation-thread:{tenant_id}:fp:{fingerprint}"


def deliver_keyed(req: Posts) -> str:
    """Post or edit req's keyed messages in the alert's thread, under the thread's claim: "posted" (something went
    to Slack), "already" (nothing changed), "stale" (the thread shows a newer state), "busy" (another send for the
    thread holds the claim: retry), "pending" (no thread yet) or "failed" (Slack took none of it)."""
    cache = Cache()
    claim = thread_claim(req.tenant_id, req.fingerprint)
    token = cache.claim_owned(claim, CLAIM_SECONDS)
    if token is None:
        if req.state_seq is not None:
            # Kept as the thread's newest state not yet shown: the holder draws it before it lets go, so the last
            # state is not lost even when llm-server's retries give up first.
            with Session(sync_engine) as session:
                _store_pending(session, req)
        return "busy"
    try:
        outcome = _deliver_keyed(req)
        if outcome != "pending":  # no thread yet: the waiting row is this very send
            _drain_newer_state(req)
        return outcome
    finally:
        # Only this send's own claim: one that outlived CLAIM_SECONDS may have been taken by the next send since.
        cache.release_owned(claim, token)


# How many newer states a claim holder draws after its own send before it lets go.
DRAIN_LIMIT = 5


def _take_newer_state(tenant_id, fingerprint, shown_seq) -> Optional[Posts]:
    """Takes the thread's waiting state send (stored while another send held the claim, or before the thread
    existed) when it is newer than shown_seq; a waiting send no newer is dropped."""
    with Session(sync_engine) as session:
        row = (
            session.query(SentNotifications)
            .filter_by(tenant_id=_uuid(tenant_id), fingerprint=f"{PENDING_PREFIX}{fingerprint}:{STATE_PENDING}")
            .filter(delivery_records.of_kind(delivery_records.REMEDIATION_PENDING))
            .first()
        )
        if row is None:
            return None
        try:
            waiting = Posts.model_validate_json(row.slack_metadata or "{}")
        except ValueError:
            waiting = None
        session.delete(row)
        session.commit()
    if waiting is None or waiting.state_seq is None or (shown_seq is not None and waiting.state_seq <= shown_seq):
        return None
    return waiting


def _drain_newer_state(req: Posts) -> None:
    """Under the claim, after req: draws the newer states that arrived for the thread while it was posting."""
    shown = req.state_seq
    for _ in range(DRAIN_LIMIT):
        newer = _take_newer_state(req.tenant_id, req.fingerprint, shown)
        if newer is None:
            return
        try:
            outcome = _deliver_keyed(newer)
        except Exception:
            LOG.warning("remediation thread for %s: a newer state was not drawn", req.event_id, exc_info=True)
            with Session(sync_engine) as session:
                _store_pending(session, newer)  # kept for the next send or llm-server's retry
            return
        if outcome == "pending":
            return  # stored again, waiting for the thread
        shown = newer.state_seq


def _store_pending(session, req: Posts) -> None:
    """Keep req until the alert's thread exists. A state send replaces the thread's older waiting state send: only
    the newest state is drawn once the thread appears."""
    tenant = _uuid(req.tenant_id)
    queued = req.model_copy(update={"queued_at": req.queued_at or time.time()})
    if req.state_seq is not None:
        fingerprint = f"{PENDING_PREFIX}{req.fingerprint}:{STATE_PENDING}"
        row = (
            session.query(SentNotifications)
            .filter_by(tenant_id=tenant, fingerprint=fingerprint)
            .filter(delivery_records.of_kind(delivery_records.REMEDIATION_PENDING))
            .first()
        )
        if row is not None:
            try:
                waiting = Posts.model_validate_json(row.slack_metadata or "{}")
            except ValueError:
                waiting = None
            if waiting is not None and (waiting.state_seq or 0) > req.state_seq:
                return  # a newer state already waits
            row.slack_metadata = queued.model_copy(
                update={"queued_at": (waiting.queued_at if waiting else None) or queued.queued_at}
            ).model_dump_json()
            session.commit()
            return
    else:
        fingerprint = f"{PENDING_PREFIX}{req.fingerprint}:{uuid.uuid4()}"
    session.add(
        SentNotifications(
            id=uuid.uuid4(),
            created_at=utc_now(),
            tenant_id=tenant,
            account_id=_uuid(req.account_id),
            fingerprint=fingerprint,
            kind=delivery_records.REMEDIATION_PENDING,
            platform="slack",
            status=delivery_records.PENDING,
            slack_metadata=queued.model_dump_json(),
        )
    )
    session.commit()


def _note_episode(session, req: Posts, team_id) -> None:
    """Remember, for a duplicate's thread, which alert's thread holds the episode's conversation."""
    main = req.main_fingerprint
    if not main or main == req.fingerprint:
        return
    tenant = _uuid(req.tenant_id)
    fingerprint = EPISODE_PREFIX + req.fingerprint
    row = (
        session.query(SentNotifications)
        .filter_by(tenant_id=tenant, fingerprint=fingerprint)
        .filter(delivery_records.of_kind(delivery_records.REMEDIATION_MARKER))
        .first()
    )
    state = json.dumps({"main_fingerprint": main, "account_id": req.account_id})
    if row is None:
        session.add(
            SentNotifications(
                id=uuid.uuid4(),
                created_at=utc_now(),
                tenant_id=tenant,
                account_id=_uuid(req.account_id),
                fingerprint=fingerprint,
                slack_team_id=team_id,
                kind=delivery_records.REMEDIATION_MARKER,
                platform="slack",
                team_id=team_id,
                slack_metadata=state,
            )
        )
    elif row.slack_metadata != state:
        row.slack_metadata = state
    try:
        session.commit()
    except IntegrityError:
        session.rollback()


def episode_main(session, tenant_id, fingerprint) -> Optional[str]:
    """The main alert of the episode a duplicate's thread belongs to, as the last state send said; None for a main
    alert's own thread or one never synced."""
    if not (tenant_id and fingerprint):
        return None
    row = (
        session.query(SentNotifications.slack_metadata)
        .filter_by(tenant_id=_uuid(tenant_id), fingerprint=EPISODE_PREFIX + str(fingerprint))
        .filter(SentNotifications.kind == delivery_records.REMEDIATION_MARKER)
        .first()
    )
    main = _marker_state(row).get("main_fingerprint") if row else None
    return main if main and main != fingerprint else None


def _deliver_keyed(req: Posts) -> str:
    tenant = _uuid(req.tenant_id)
    keys = [message_marker(req.fingerprint, p.key) for p in req.posts if p.key]
    with Session(sync_engine) as session:
        alert = latest_slack_notification(session, req.fingerprint, tenant, _uuid(req.account_id))
        channel = json.loads(alert.slack_metadata or "{}").get("channel") if alert else None
        installation = load_installation(session, req.tenant_id, "slack") if channel else None
        if not installation:
            _store_pending(session, req)
            return "pending"
        token, thread_ts, team_id = installation.token, alert.slack_thread_id, alert.slack_team_id
        channel_name = alert.destination_name
        markers = {
            row.fingerprint: (row.id, row.message_id, _marker_state(row), row.created_at)
            for row in session.query(SentNotifications)
            .filter(SentNotifications.tenant_id == tenant, SentNotifications.fingerprint.in_(keys))
            .filter(SentNotifications.kind == delivery_records.REMEDIATION_MARKER)
            .all()
        }
        if req.state_seq is not None and any(
            int(state.get("state_seq") or 0) > req.state_seq for _, _, state, _ in markers.values()
        ):
            return "stale"
        _note_episode(session, req, team_id)
    sent = delivery_records.Dispatch(
        tenant_id=tenant,
        kind=delivery_records.REMEDIATION,
        account_id=_uuid(req.account_id),
        fingerprint=req.fingerprint or None,
        event_id=delivery_records.as_uuid(req.event_id),
    )
    rows = []
    tried = went = 0
    for post in req.posts:
        if not post.key:
            continue
        marker = message_marker(req.fingerprint, post.key)
        digest = _content_hash(post, req)
        found = markers.get(marker)
        if found is None:
            if post.only_if_posted:
                continue
            marker_id = uuid.uuid4()
            try:
                with Session(sync_engine) as session:
                    session.add(
                        SentNotifications(
                            id=marker_id,
                            created_at=utc_now(),
                            tenant_id=tenant,
                            account_id=_uuid(req.account_id),
                            fingerprint=marker,
                            slack_team_id=team_id,
                            kind=delivery_records.REMEDIATION_MARKER,
                            platform="slack",
                            team_id=team_id,
                            slack_metadata=json.dumps({"state_seq": req.state_seq}),
                        )
                    )
                    session.commit()
            except IntegrityError:
                return "busy"  # another writer is posting this very message (the claim failed open)
            tried += 1
            ts = _post_keyed(token, channel, thread_ts, post, req, sent, team_id, rows)
            if ts is None:
                with Session(sync_engine) as session:
                    session.query(SentNotifications).filter_by(id=marker_id).delete()
                    session.commit()
                continue
            went += 1
            _save_marker(marker_id, ts, channel, thread_ts, req.state_seq, digest)
            continue
        marker_id, ts, state, created_at = found
        if not ts and created_at and created_at > utc_now() - timedelta(seconds=CLAIM_SECONDS):
            return "busy"  # marked a moment ago and still being posted by another writer (the claim failed open)
        if not post.edit and ts:
            continue  # posted once
        if ts and state.get("hash") == digest:
            if req.state_seq is not None and state.get("state_seq") != req.state_seq:
                _save_marker(marker_id, ts, channel, thread_ts, req.state_seq, digest)
            continue
        tried += 1
        new_ts = _edit_keyed(token, channel, thread_ts, ts, post, req, sent, team_id, rows)
        if new_ts is None:
            continue
        went += 1
        _save_marker(marker_id, new_ts, channel, thread_ts, req.state_seq, digest)
    for row in rows:
        row.destination_name = channel_name
    delivery_records.record_sync(rows, engine=sync_engine)
    if tried and not went:
        return "failed"
    return "posted" if went else "already"


def _save_marker(marker_id, ts, channel, thread_ts, state_seq, digest) -> None:
    with Session(sync_engine) as session:
        row = session.get(SentNotifications, marker_id)
        if row is None:
            return
        row.message_id, row.destination, row.thread_id = ts, channel, thread_ts
        row.slack_metadata = json.dumps({"state_seq": state_seq, "hash": digest})
        session.commit()


def _post_keyed(token, channel, thread_ts, post: Post, req: Posts, sent, team_id, rows) -> Optional[str]:
    """Posts one keyed message in the thread; its ts, or None when Slack took none of it."""
    for attempt in range(2):
        try:
            response = slack_app.client.reply_in_thread(
                token=token,
                channel_id=channel,
                thread_ts=thread_ts,
                text=_cap(slack_text(post), 300),
                blocks=blocks(post),
                metadata=_metadata(post, req),
            )
            ts = response.get("ts") if response else None
            _record_post(rows, sent, team_id, channel, thread_ts, post, ts=ts)
            return ts or None
        except SlackApiError as e:
            if attempt == 0 and _rate_limited_wait(e):
                continue
            LOG.warning("remediation thread for %s: a post failed: %s", req.event_id, e)
            _record_post(rows, sent, team_id, channel, thread_ts, post, failed=True, refusal=e.response)
        except Exception as e:
            LOG.warning("remediation thread for %s: a post failed: %s", req.event_id, e)
            _record_post(rows, sent, team_id, channel, thread_ts, post, failed=True)
        return None
    return None


def _edit_keyed(token, channel, thread_ts, ts, post: Post, req: Posts, sent, team_id, rows) -> Optional[str]:
    """Edits one keyed message in place; a message Slack no longer has (or won't let the app edit) is posted again.
    The message's ts after it, or None when neither went in."""
    if not ts:
        return _post_keyed(token, channel, thread_ts, post, req, sent, team_id, rows)
    for attempt in range(2):
        try:
            slack_app.client.chat_update(
                token=token,
                channel_id=channel,
                ts=ts,
                text=_cap(slack_text(post), 300),
                blocks=blocks(post),
                metadata=_metadata(post, req),
            )
            return ts
        except SlackApiError as e:
            if attempt == 0 and _rate_limited_wait(e):
                continue
            error = slack_error(e)
            if error in REPOST_ERRORS:
                LOG.info("remediation thread for %s: %s, posting it again", req.event_id, error)
                return _post_keyed(token, channel, thread_ts, post, req, sent, team_id, rows)
            LOG.warning("remediation thread for %s: an edit failed: %s", req.event_id, e)
        except Exception as e:
            LOG.warning("remediation thread for %s: an edit failed: %s", req.event_id, e)
        return None
    return None


def slack_error(e: SlackApiError) -> str:
    """The error code of Slack's refusal ("message_not_found", ...), or ""."""
    response = e.response
    if response is None:
        return ""
    data = getattr(response, "data", None)
    if isinstance(data, dict) and data.get("error"):
        return str(data["error"])
    try:
        return str(response["error"] or "")
    except Exception:
        return ""


def _rate_limited_wait(e: SlackApiError) -> bool:
    """Waits out a short rate limit and says so; False for any other refusal."""
    if e.response is None or e.response.status_code != 429:
        return False
    wait = int((e.response.headers or {}).get("Retry-After", 0) or 0)
    if wait > RATE_LIMIT_WAIT:
        return False
    time.sleep(wait or 1)
    return True


def flush_pending(tenant_id, fingerprint) -> int:
    """Post the sends that waited for this alert's Slack thread, oldest first. Returns how many were posted."""
    if not (tenant_id and fingerprint):
        return 0
    with Session(sync_engine) as session:
        if not loop_on(session, tenant_id):
            return 0  # only a tenant with the loop on has posts waiting
        waiting = (
            session.query(SentNotifications)
            .filter(
                SentNotifications.tenant_id == _uuid(tenant_id),
                SentNotifications.fingerprint.startswith(f"{PENDING_PREFIX}{fingerprint}:", autoescape=True),
                delivery_records.of_kind(delivery_records.REMEDIATION_PENDING),
            )
            .order_by(SentNotifications.created_at)
            .all()
        )
        sends = [(row.id, row.slack_metadata, row.created_at) for row in waiting]
    posted = 0
    oldest = utc_now() - PENDING_LIFETIME
    for row_id, meta, created_at in sends:
        try:
            req = Posts.model_validate_json(meta or "{}")
            if not req.queued_at:
                # Queued before the time was kept: it has waited since its row was written.
                req = req.model_copy(update={"queued_at": created_at.replace(tzinfo=timezone.utc).timestamp()})
            queued_at = datetime.fromtimestamp(req.queued_at, timezone.utc).replace(tzinfo=None)
            # A plan that waited a day for its thread is stale: dropped, not posted.
            outcome = "expired" if queued_at < oldest else deliver(req)
        except Exception as e:
            LOG.warning("remediation thread for %s: a waiting send failed: %s", fingerprint, e)
            continue
        if outcome in ("failed", "busy") or (outcome == "pending" and req.state_seq is not None):
            continue  # keep it waiting: the next alert in this thread tries it again (a state send keeps its row)
        posted += outcome == "posted"
        with Session(sync_engine) as session:
            session.query(SentNotifications).filter_by(id=row_id).delete()
            session.commit()
    return posted


def linked_user(session, tenant_id, slack_user_id) -> Optional[str]:
    """The user Identity Sync linked to this Slack user id in this tenant. Keyed on Slack's user id, which a
    workspace member can't change, not on the profile email, which can be edited."""
    row = session.execute(
        text("""SELECT mapped_user_id FROM integration_user_accounts
                WHERE tenant_id = :tenant AND integration_type = 'slack' AND external_user_id = :slack_user
                  AND is_active AND mapped_user_id IS NOT NULL
                ORDER BY updated_at DESC LIMIT 1"""),
        {"tenant": str(tenant_id), "slack_user": str(slack_user_id)},
    ).first()
    return str(row[0]) if row and row[0] else None


@dataclass
class Outcome:
    message: str
    # True when the step is decided (now or earlier) or its plan replaced, so its buttons should go.
    settled: bool


def decide(token, option, slack_user_id) -> Outcome:
    """Records the clicker's decision through llm-server and says what happened, in words to show them."""
    brand = settings.urls.branding_name
    parsed = parse_token(token)
    slot = SLOTS.get(str(option or "").lower())
    if not (parsed and slot):
        LOG.warning("Refusing a remediation click with unreadable data")
        return Outcome(CANT_READ, False)
    tenant_id, account_id, event_id, plan_version, step = parsed
    with Session(sync_engine) as session:
        user_id = linked_user(session, tenant_id, slack_user_id)
    if not user_id:
        return Outcome(NOT_LINKED.format(brand=brand), False)
    from notifications_server.services.events import Events  # events imports message, which imports this

    try:
        response = requests.post(
            settings.services.llm_server + EXECUTE_PATH,
            headers=Events._with_llm_auth({"x-user-id": user_id, "x-tenant-id": tenant_id}),
            json={
                "input": {
                    "request": {
                        "account_id": account_id,
                        "event_id": event_id,
                        "step": step,
                        "slot": slot,
                        "plan_version": plan_version,
                        "source": "slack",
                    }
                }
            },
            # A run waits for its result, past llm-server's own send timeout (RunSendTimeout, 10 minutes).
            timeout=RUN_WAIT_SECONDS,
        )
    except requests.Timeout:
        return Outcome(STILL_RUNNING, False)
    except requests.RequestException as e:
        LOG.warning("remediation click: execute unreachable: %s", e)
        return Outcome(UNREACHABLE.format(brand=brand), False)
    try:
        body = response.json()
    except ValueError:
        body = None
    data = body.get("data") if isinstance(body, dict) else None
    errors = body.get("errors") if isinstance(body, dict) else None
    message = ""
    if isinstance(errors, list) and errors:
        first = errors[0]
        message = (first.get("message", "") if isinstance(first, dict) else str(first)).removeprefix("remediation: ")
    return answer(response.status_code, data if isinstance(data, dict) else {}, message, slot)


def answer(status_code: int, data: dict, message: str, slot: str) -> Outcome:
    brand = settings.urls.branding_name
    if status_code == 200:
        if slot == "decline":
            return Outcome("Declined.", True)
        status = data.get("status")
        if status == "Success":
            return Outcome("Ran and exited 0. NudgeBee checks on it next.", True)
        if status == "Failed":
            return Outcome(f"Ran and failed: {_cap(data.get('error'), 800)}", True)
        return Outcome(f"The outcome is unknown: {_cap(data.get('error'), 800)} Check before running it again.", True)
    if status_code == 409 and "unknown outcome" in message:
        # Slack has no "I checked, run it again"; the alert's page does.
        return Outcome(
            f"Not run: {message.split(' Check the cluster')[0]} Check, then run it from the alert in {brand}.", False
        )
    if status_code == 409 and "already decided" in message:
        # Only the clicker is told: the click that decided it owns the message (two quick clicks both settle
        # from the same copy of it, and the refusal must not overwrite the outcome).
        return Outcome(f"Not recorded: {message}", False)
    if status_code == 409 and "plan changed" in message:
        # This message's plan is not the alert's any more: its buttons are done.
        return Outcome(f"Not recorded: {message}", True)
    if status_code == 409:
        # The same command is running, or the step can't be decided now: the buttons stay for a later click.
        return Outcome(f"Not recorded: {message or 'try again later'}", False)
    if status_code == 403:
        return Outcome(f"You can't do that: {message or 'it needs write access on this account'}.", False)
    return Outcome(f"Not recorded: {message or 'the request was refused'}", False)
