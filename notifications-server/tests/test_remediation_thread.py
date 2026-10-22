"""The remediation loop in an alert's Slack thread (#40734): llm-server's plan posts go in the alert's thread, a
step's Run and Decline are main's approval buttons with a token in the message metadata, and a click is decided
by llm-server as the clicker's Identity Sync user."""

import json
import uuid
from datetime import datetime
from types import SimpleNamespace

import pytest
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from sqlalchemy.pool import StaticPool

from notifications_server.configs import settings
from notifications_server.models.models import Base, FeatureFlag, SentNotifications
from notifications_server.services import remediation_thread as rt
from notifications_server.services.actions_common import SlackInteractiveActionsService
from notifications_server.utils.transformer import Transformer

TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
EVENT = "eeeeeeee-5555-4555-8555-555555555555"
USER = "ffffffff-6666-4666-8666-666666666666"
TEAM, CHANNEL, THREAD, FP = "T1", "C1", "1700000000.000100", "fp-1"
TOKEN = f"remediation:{TENANT}:{ACCOUNT}:{EVENT}:pv-1:1"


@pytest.fixture
def db(monkeypatch):
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__, FeatureFlag.__table__])
    flag(engine, TENANT)
    with engine.begin() as c:
        c.execute(
            text(
                "CREATE TABLE integration_user_accounts (tenant_id text, integration_type text, external_user_id text,"
                " is_active boolean, mapped_user_id text, updated_at text)"
            )
        )
    monkeypatch.setattr(rt, "sync_engine", engine)
    return engine


def flag(engine, tenant, account=None):
    """The tenant's remediation-loop flag, tenant-wide, or an account row when account is given."""
    with Session(engine) as s:
        s.add(
            FeatureFlag(
                id=uuid.uuid4(),
                feature_id=rt.FEATURE,
                tenant_id=uuid.UUID(tenant),
                account_id=uuid.UUID(account) if account else None,
                status="enabled",
                created_at=datetime(2026, 10, 20),
            )
        )
        s.commit()


class FakeCache:
    claims = set()

    def claim_card_action(self, key, ttl):
        if key in FakeCache.claims:
            return False
        FakeCache.claims.add(key)
        return True

    def release_card_action(self, key):
        FakeCache.claims.discard(key)


@pytest.fixture(autouse=True)
def cache(monkeypatch):
    FakeCache.claims = set()
    monkeypatch.setattr(rt, "Cache", FakeCache)
    return FakeCache


@pytest.fixture
def slack(monkeypatch):
    sent = []

    def reply_in_thread(**kwargs):
        if "FAIL" in kwargs.get("text", ""):
            raise RuntimeError("channel_not_found")
        sent.append(kwargs)
        return {"ts": f"2.{len(sent)}"}

    monkeypatch.setattr(rt, "slack_app", SimpleNamespace(client=SimpleNamespace(reply_in_thread=reply_in_thread)))
    monkeypatch.setattr(rt, "load_installation", lambda session, tenant, platform: SimpleNamespace(token="xoxb"))
    return sent


def alert_row(engine):
    with Session(engine) as s:
        s.add(
            SentNotifications(
                id=uuid.uuid4(),
                created_at=datetime(2026, 10, 19),
                tenant_id=uuid.UUID(TENANT),
                account_id=uuid.UUID(ACCOUNT),
                fingerprint=FP,
                slack_team_id=TEAM,
                slack_thread_id=THREAD,
                slack_metadata=json.dumps({"channel": CHANNEL}),
            )
        )
        s.commit()


def posts(key="plan:e:pv-1", texts=("*Proposed remediation*", "*1.* Restart")):
    return rt.Posts(
        tenant_id=TENANT,
        account_id=ACCOUNT,
        event_id=EVENT,
        fingerprint=FP,
        key=key,
        posts=[rt.Post(text=t, token=TOKEN if i else None) for i, t in enumerate(texts)],
    )


def test_tokens():
    assert rt.parse_token(TOKEN) == (TENANT, ACCOUNT, EVENT, "pv-1", 1)
    for bad in [
        None,
        "auto_optimize:x",
        f"remediation:{TENANT}:{ACCOUNT}:{EVENT}:pv-1",
        "remediation:a:b:c:pv:1",
        f"remediation:{TENANT}:{ACCOUNT}:{EVENT}::1",
        f"remediation:{TENANT}:{ACCOUNT}:{EVENT}:pv:-1",
    ]:
        assert rt.parse_token(bad) is None, bad


def test_a_step_with_a_token_gets_main_approval_buttons():
    blocks = rt.blocks(rt.Post(text="*1.* Restart", token=TOKEN))
    actions = blocks[-1]
    assert actions["type"] == "actions"
    run, decline = actions["elements"]
    assert run["text"]["text"] == "Run" and run["confirm"], "Run asks before it changes the account"
    for button, status in ((run, "run"), (decline, "decline")):
        body = json.loads(button["value"])["body"]
        assert body == {"action_name": "workflow_approval_action", "action_params": {"status": status}}
    assert TOKEN not in json.dumps(blocks), "the token is in the metadata, not a button"
    assert [b["type"] for b in rt.blocks(rt.Post(text="header"))] == ["section"]


def test_a_post_is_markdown_converted_as_an_investigation_is():
    text = (
        "### Proposed fix\n**Root cause:** `TALLY_MODE` is turbo\n- **Undo:** `kubectl x`\n```\nkubectl patch cm a\n```"
    )
    section = rt.blocks(rt.Post(text=text))[0]["text"]["text"]
    assert section == Transformer.markdown_to_slack_markdown(text)
    assert "*Proposed fix*" in section and "*Root cause:*" in section and "• *Undo:*" in section
    assert "```\nkubectl patch cm a\n```" in section and "`TALLY_MODE`" in section


def test_a_plan_is_posted_once_in_the_alerts_thread(db, slack):
    alert_row(db)
    assert rt.deliver(posts()) == "posted"
    assert [p["thread_ts"] for p in slack] == [THREAD, THREAD]
    assert slack[0]["metadata"] is None
    assert slack[1]["metadata"] == {"event_type": "remediation_step", "event_payload": {"token": TOKEN}}
    assert rt.deliver(posts()) == "already", "a plan version is posted once"
    assert len(slack) == 2
    assert (
        rt.deliver(posts(key=None, texts=("Step 1 (Restart) was declined, from chat.",))) == "posted"
    ), "notes have no key"
    assert len(slack) == 3


def test_one_failing_post_does_not_hold_back_the_rest(db, slack):
    """Review 3 bug 4: a post that Slack refuses no longer stops every later post for the alert."""
    alert_row(db)
    assert rt.deliver(posts(texts=("FAIL header", "*1.* Restart", "*2.* Verify"))) == "posted"
    assert [p["text"] for p in slack] == ["*1.* Restart", "*2.* Verify"]


def test_a_send_slack_takes_none_of_is_not_marked_posted(db, slack):
    """So llm-server's retry posts it, rather than finding it "already" posted."""
    alert_row(db)
    assert rt.deliver(posts(texts=("FAIL header", "FAIL step"))) == "failed"
    assert rt.deliver(posts()) == "posted"


def test_a_retry_while_the_first_send_still_posts_does_not_repost(db, slack, monkeypatch):
    """Review 4: a send that outlives llm-server's 15 s wait must not be posted again by its retry."""
    alert_row(db)
    retried = []
    real = slack_reply = rt.slack_app.client.reply_in_thread

    def slow_reply(**kwargs):
        if not retried:
            retried.append(rt.deliver(posts()))  # llm-server's retry arrives while this send posts
        return slack_reply(**kwargs)

    monkeypatch.setattr(rt.slack_app.client, "reply_in_thread", slow_reply)
    assert rt.deliver(posts()) == "posted"
    assert retried == ["already"]
    assert len(slack) == 2, "posted once"
    monkeypatch.setattr(rt.slack_app.client, "reply_in_thread", real)
    assert rt.deliver(posts()) == "already", "and its marker stays once it is done"


def test_a_rate_limited_post_is_retried_once(db, slack, monkeypatch):
    from slack_sdk.errors import SlackApiError

    alert_row(db)
    limited = []
    real = rt.slack_app.client.reply_in_thread

    def reply(**kwargs):
        if not limited:
            limited.append(1)
            raise SlackApiError("ratelimited", SimpleNamespace(status_code=429, headers={"Retry-After": "0"}, data={}))
        return real(**kwargs)

    monkeypatch.setattr(rt.slack_app.client, "reply_in_thread", reply)
    monkeypatch.setattr(rt.time, "sleep", lambda s: None)
    assert rt.deliver(posts(texts=("*Proposed remediation*",))) == "posted"
    assert len(slack) == 1


def test_posts_wait_for_the_alerts_thread(db, slack):
    assert rt.deliver(posts()) == "pending"
    assert slack == []
    alert_row(db)
    assert rt.flush_pending(TENANT, FP) == 1
    assert len(slack) == 2
    assert rt.flush_pending(TENANT, FP) == 0, "posted once"


def test_a_waiting_send_slack_refuses_keeps_waiting(db, slack):
    assert rt.deliver(posts(texts=("FAIL header", "FAIL step"))) == "pending"
    alert_row(db)
    assert rt.flush_pending(TENANT, FP) == 0
    assert rt.flush_pending(TENANT, FP) == 0, "still waiting, tried again"
    with Session(db) as session:
        assert (
            session.query(SentNotifications).filter(SentNotifications.fingerprint.startswith(rt.PENDING_PREFIX)).count()
            == 1
        )


def link(engine, slack_user="U1"):
    with engine.begin() as c:
        c.execute(
            text("INSERT INTO integration_user_accounts VALUES (:t, 'slack', :u, true, :m, '2026-10-19')"),
            {"t": TENANT, "u": slack_user, "m": USER},
        )


class FakeResponse:
    def __init__(self, status, body):
        self.status_code, self._body = status, body

    def json(self):
        return self._body


def test_a_click_is_decided_by_llm_server_as_the_linked_user(db, monkeypatch):
    calls = []
    monkeypatch.setattr(
        rt.requests,
        "post",
        lambda url, headers, json, timeout: calls.append((url, headers, json))
        or FakeResponse(200, {"data": {"status": "Success"}}),
    )
    out = rt.decide(TOKEN, "run", "U1")
    assert not out.settled and "Identity Sync" in out.message, "an unlinked Slack user decides nothing"
    assert calls == []
    link(db)
    out = rt.decide(TOKEN, "run", "U1")
    assert out.settled and "exited 0" in out.message
    url, headers, body = calls[0]
    assert url.endswith(rt.EXECUTE_PATH)
    assert headers["x-user-id"] == USER and headers["x-tenant-id"] == TENANT
    assert body["input"]["request"] == {
        "account_id": ACCOUNT,
        "event_id": EVENT,
        "step": 1,
        "slot": "execute",
        "plan_version": "pv-1",
        "source": "slack",
    }, "the step and plan version, never a command: llm-server reads the step's own"
    assert not rt.decide(TOKEN, "approve", "U1").settled


@pytest.mark.parametrize(
    "status,message,settled",
    [
        (409, "remediation: this step of the plan was already decided: it ran.", False),
        (409, "remediation: the plan changed since you reviewed it. Review it again.", True),
        (
            409,
            "remediation: the last run of this command on this account has an unknown outcome. Check the cluster",
            False,
        ),
        (403, "remediation: write access is required", False),
        (400, "remediation: the plan changed", False),
        # Review of #41596: only a changed plan ends a step's buttons; a busy command leaves them for later.
        (409, "remediation: this command is already running on this account. Wait for its result.", False),
        (409, "remediation: this event does not belong to that account.", False),
    ],
)
def test_answers(status, message, settled):
    out = rt.answer(status, {}, message.removeprefix("remediation: "), "execute")
    assert out.settled is settled


def test_a_remediation_click_never_reaches_the_workflow_approval(monkeypatch):
    decided, edits, replies, tasks, closed = [], [], [], [], []
    monkeypatch.setattr(
        rt, "decide", lambda token, status, user: decided.append((token, status, user)) or rt.Outcome("Declined.", True)
    )
    common = SimpleNamespace(
        app_id="A1",
        update_slack_message_with_blocks=lambda *a: edits.append(a),
        post_slack_ephemeral_response=lambda *a: replies.append(a),
    )
    service = SimpleNamespace(
        common_service=common,
        engine=None,
        slack_app=None,
        teams_app=None,
        action_service=SimpleNamespace(handle_approval_action=lambda **k: pytest.fail("reached the workflow approval")),
    )
    monkeypatch.setattr(
        "notifications_server.services.actions_common.SlackActionsBaseService",
        lambda *a: SimpleNamespace(common_service=common, close=lambda: closed.append(1)),
    )
    service.handle_remediation_approval = SlackInteractiveActionsService.handle_remediation_approval.__get__(service)
    message = {
        "ts": "2.1",
        "thread_ts": THREAD,
        "metadata": {"event_payload": {"token": TOKEN}},
        "blocks": [{"type": "section"}, {"type": "actions"}],
    }
    SlackInteractiveActionsService.handle_approval_action(
        service,
        CHANNEL,
        TEAM,
        "U1",
        None,
        {"message": message},
        {"status": "decline"},
        SimpleNamespace(add_task=lambda fn, *a: tasks.append((fn, a))),
    )
    assert decided == [] and len(tasks) == 1, "decided after Slack's answer, in the background"
    fn, args = tasks[0]
    assert fn == rt.RUNS.submit, "on the remediation pool, not the server's request threads"
    fn(*args).result()
    assert decided == [(TOKEN, "decline", "U1")]
    blocks = edits[0][3]
    assert [b["type"] for b in blocks] == ["section", "context"], "the buttons give way to the outcome"
    assert "<@U1>: Declined." in json.dumps(blocks)
    assert replies[-1][3] == "Declined."
    assert closed == [1], "the click's own session is closed"

    # A failure in the background is logged and told to the clicker, and leaves the buttons.
    def boom(*a):
        raise RuntimeError("database down")

    monkeypatch.setattr(rt, "decide", boom)
    tasks.clear()
    SlackInteractiveActionsService.handle_approval_action(
        service,
        CHANNEL,
        TEAM,
        "U1",
        None,
        {"message": message},
        {"status": "decline"},
        SimpleNamespace(add_task=lambda fn, *a: tasks.append((fn, a))),
    )
    fn, args = tasks[0]
    fn(*args).result()
    assert len(edits) == 1, "nothing settled"
    assert "Couldn't reach" in replies[-1][3]


def test_the_loop_is_on_for_the_whole_tenant_only(db):
    other = "dddddddd-4444-4444-8444-444444444444"
    flag(db, other, account=ACCOUNT)
    with Session(db) as session:
        assert rt.loop_on(session, TENANT)
        assert rt.loop_on(session, uuid.UUID(TENANT))
        assert not rt.loop_on(session, other), "an account row does not turn it on for the tenant"
        assert not rt.loop_on(session, None)


def test_a_tenant_with_the_loop_off_has_no_posts_waiting(db, slack):
    assert rt.deliver(posts()) == "pending"
    alert_row(db)
    with Session(db) as session:
        session.query(FeatureFlag).delete()
        session.commit()
    assert rt.flush_pending(TENANT, FP) == 0
    assert slack == []


def test_a_run_past_the_wait_is_reported_as_still_running(db, monkeypatch):
    """Review 4: a long run is not reported to the clicker as NudgeBee being unreachable."""
    link(db)

    def slow(*a, **k):
        raise rt.requests.Timeout()

    monkeypatch.setattr(rt.requests, "post", slow)
    out = rt.decide(TOKEN, "run", "U1")
    assert out.message == rt.STILL_RUNNING and not out.settled
    assert rt.RUN_WAIT_SECONDS > 600, "longer than llm-server's RunSendTimeout"
