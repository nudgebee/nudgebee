"""The remediation loop in the alert's Slack thread (#40734).

llm-server posts an alert's plan here: a header, then one message per step. A step NudgeBee runs carries Run
and Decline, main's approval buttons; its message metadata holds a token naming the step,
``remediation:<tenant>:<account>:<event>:<plan version>:<step>``. It is not a credential: a click is decided by
llm-server's remediation endpoint, the one the web panel uses, as the NudgeBee user Identity Sync links to the
clicker's Slack user id, and llm-server checks that user's access, the plan version and the step's one
decision. The step's buttons are then replaced by the outcome. Posts for an alert with no Slack thread yet
wait, stored, and go in when the thread is created."""

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


class Post(BaseModel):
    text: str
    token: Optional[str] = None


class Posts(BaseModel):
    """One send from llm-server: messages for one alert's thread, in order."""

    tenant_id: str
    account_id: str
    event_id: str
    fingerprint: str = ""
    # Names the send so it is posted once (a plan's version); empty for a note.
    key: Optional[str] = None
    posts: List[Post]
    # When the send first had to wait for the alert's thread (epoch seconds),
    # kept while it waits again.
    queued_at: Optional[float] = None


# The tenant feature flag that turns the remediation loop on, for every account of the tenant: llm-server's
# events.RemediationLoopFeature.
FEATURE = "EVENT_REMEDIATION_LOOP"


def loop_on(session, tenant_id) -> bool:
    """Whether tenant_id has the remediation loop on (its tenant-wide feature_flag row)."""
    return bool(tenant_id) and is_feature_enabled(session, FEATURE, str(tenant_id))


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


def blocks(post: Post) -> list:
    """A post as Slack blocks: its text, and main's approval buttons (Run, Decline) when it has a token. The
    token is in the message metadata, as main's approval messages keep it."""
    out = [{"type": "section", "text": {"type": "mrkdwn", "text": _cap(slack_text(post))}}]
    if post.token:
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
    thread yet), or "failed" (Slack took none of its posts)."""
    claim = f"remediation-thread:{req.tenant_id}:{req.key}" if req.key else None
    cache = Cache()
    if claim and not cache.claim_card_action(claim, CLAIM_SECONDS):
        return "already"  # the same send is being posted; a retry after llm-server's timeout must not repost
    try:
        return _deliver(req)
    finally:
        if claim:
            cache.release_card_action(claim)


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
        if outcome == "failed":
            continue  # keep it waiting: the next alert in this thread tries it again
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
