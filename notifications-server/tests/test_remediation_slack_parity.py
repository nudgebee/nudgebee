"""The remediation loop's Slack thread drawn from the server's state (#40734): keyed messages posted once and edited
in place in state order, the same state in every thread of a duplicate episode, and the thread's buttons decided by
the server as the clicker's Identity Sync user. Slack is a recording fake: nothing is sent anywhere."""

import json
import uuid
from datetime import datetime, timedelta
from types import SimpleNamespace

import pytest
from slack_sdk.errors import SlackApiError
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from sqlalchemy.pool import StaticPool

from notifications_server.models.models import Base, FeatureFlag, SentNotifications
from notifications_server.services import remediation_actions as ra
from notifications_server.services import remediation_thread as rt
from notifications_server.services.actions_common import SlackInteractiveActionsService
from notifications_server.services.events import Events
from notifications_server.utils.datetime_utils import utc_now

TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
EVENT = "eeeeeeee-5555-4555-8555-555555555555"
USER = "ffffffff-6666-4666-8666-666666666666"
RUN = "bbbbbbbb-2222-4222-8222-222222222222"
TEAM, CHANNEL, THREAD, FP, DUP_FP = "T1", "C1", "1700000000.000100", "fp-main", "fp-dup"
DUP_THREAD = "1700000000.000900"
PLAN_TOKEN = f"remediation-plan:{TENANT}:{ACCOUNT}:{EVENT}:pv-2"
RUN_TOKEN = f"remediation-run:{TENANT}:{ACCOUNT}:{EVENT}:{RUN}"


# --- fixtures ---------------------------------------------------------------------------------------------------


@pytest.fixture
def db(monkeypatch):
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__, FeatureFlag.__table__])
    with engine.begin() as c:
        c.execute(
            text(
                "CREATE TABLE integration_user_accounts (tenant_id text, integration_type text, external_user_id text,"
                " is_active boolean, mapped_user_id text, updated_at text)"
            )
        )
        # The production index (V1068), so a second marker of one message is refused as there.
        c.execute(
            text(
                "CREATE UNIQUE INDEX idx_marker ON sent_notifications (tenant_id, fingerprint)"
                " WHERE kind = 'remediation_marker'"
            )
        )
    with Session(engine) as s:
        s.add(
            FeatureFlag(
                id=uuid.uuid4(),
                feature_id=rt.FEATURE,
                tenant_id=uuid.UUID(TENANT),
                status="enabled",
                created_at=datetime(2026, 10, 20),
            )
        )
        s.commit()
    monkeypatch.setattr(rt, "sync_engine", engine)
    return engine


class FakeCache:
    claims = set()

    def claim_card_action(self, key, ttl):
        if key in FakeCache.claims:
            return False
        FakeCache.claims.add(key)
        return True

    def release_card_action(self, key):
        FakeCache.claims.discard(key)

    def claim_owned(self, key, ttl):
        return f"token-{key}" if self.claim_card_action(key, ttl) else None

    def release_owned(self, key, token):
        if token == f"token-{key}":
            FakeCache.claims.discard(key)


@pytest.fixture(autouse=True)
def cache(monkeypatch):
    FakeCache.claims = set()
    monkeypatch.setattr(rt, "Cache", FakeCache)
    return FakeCache


class RecordingSlack:
    """Slack's client, recording every payload and answering with fake ts values."""

    def __init__(self):
        self.calls = []
        self.fail_update = None

    def reply_in_thread(self, **kw):
        self.calls.append(("post", kw))
        return {"ts": f"9.{len(self.calls)}"}

    def chat_update(self, **kw):
        self.calls.append(("update", kw))
        if self.fail_update:
            raise SlackApiError(
                self.fail_update, SimpleNamespace(status_code=200, headers={}, data={"error": self.fail_update})
            )
        return {"ts": kw["ts"]}

    def views_open(self, **kw):
        self.calls.append(("views_open", kw))
        return {"ok": True}

    def of(self, kind):
        return [kw for k, kw in self.calls if k == kind]


@pytest.fixture
def slack(monkeypatch):
    fake = RecordingSlack()
    monkeypatch.setattr(rt, "slack_app", SimpleNamespace(client=fake))
    monkeypatch.setattr(rt, "load_installation", lambda session, tenant, platform: SimpleNamespace(token="xoxb"))
    return fake


def thread(engine, fingerprint=FP, ts=THREAD):
    with Session(engine) as s:
        s.add(
            SentNotifications(
                id=uuid.uuid4(),
                created_at=datetime(2026, 10, 19),
                tenant_id=uuid.UUID(TENANT),
                account_id=uuid.UUID(ACCOUNT),
                fingerprint=fingerprint,
                slack_team_id=TEAM,
                slack_thread_id=ts,
                slack_metadata=json.dumps({"channel": CHANNEL}),
            )
        )
        s.commit()


def link(engine, slack_user="U1"):
    with engine.begin() as c:
        c.execute(
            text("INSERT INTO integration_user_accounts VALUES (:t, 'slack', :u, true, :m, '2026-10-19')"),
            {"t": TENANT, "u": slack_user, "m": USER},
        )


def options(status="Pick a fix", buttons=None, text_="### Fix options\n**Option 1 · Roll back**"):
    if buttons is None:
        buttons = [
            rt.Button(kind="pick", label="Use fix 1", option=1, style="primary"),
            rt.Button(kind="other", label="Other fixes…"),
        ]
    return rt.Post(text=text_, key="options:e:pv-2", edit=True, token=PLAN_TOKEN, status=status, buttons=buttons)


def state(seq, *posts, fingerprint=FP, main=None):
    return rt.Posts(
        tenant_id=TENANT,
        account_id=ACCOUNT,
        event_id=EVENT,
        fingerprint=fingerprint,
        posts=list(posts) or [options()],
        state_seq=seq,
        main_fingerprint=main,
    )


def marker(engine, fingerprint=FP, key="options:e:pv-2"):
    with Session(engine) as s:
        return (
            s.query(SentNotifications)
            .filter_by(fingerprint=rt.message_marker(fingerprint, key), kind="remediation_marker")
            .one_or_none()
        )


# --- ordering ---------------------------------------------------------------------------------------------------


def test_a_keyed_message_is_posted_once_then_edited_in_place(db, slack):
    thread(db)
    assert rt.deliver(state(1)) == "posted"
    [post] = slack.of("post")
    assert post["thread_ts"] == THREAD
    assert post["metadata"] == {
        "event_type": "remediation_loop",
        "event_payload": {"token": PLAN_TOKEN, "main_fingerprint": FP},
    }
    blocks = post["blocks"]
    assert [b["type"] for b in blocks] == ["section", "context", "actions"]
    pick, other = blocks[2]["elements"]
    assert json.loads(pick["value"]) == {"k": "pick", "o": 1} and pick["action_id"] == "remediation_pick_0"
    assert PLAN_TOKEN not in json.dumps(blocks), "the token is in the metadata, never a button"
    row = marker(db)
    assert row.message_id == "9.1" and json.loads(row.slack_metadata)["state_seq"] == 1

    assert rt.deliver(state(2)) == "already", "the same content: nothing goes to Slack"
    assert len(slack.calls) == 1
    assert json.loads(marker(db).slack_metadata)["state_seq"] == 2, "the newer state number is kept"

    assert rt.deliver(state(3, options(status="Waiting for approval in Slack", buttons=[]))) == "posted"
    [update] = slack.of("update")
    assert update["ts"] == "9.1" and update["channel_id"] == CHANNEL
    assert update["metadata"]["event_payload"]["token"] == PLAN_TOKEN, "the metadata is sent again with the edit"
    assert [b["type"] for b in update["blocks"]] == ["section", "context"], "no buttons outside picking"
    assert len(slack.of("post")) == 1, "an edit, not a new post"


def test_an_older_state_is_refused(db, slack):
    thread(db)
    assert rt.deliver(state(5)) == "posted"
    assert rt.deliver(state(4, options(status="Running"))) == "stale"
    assert slack.of("update") == []
    assert json.loads(marker(db).slack_metadata)["state_seq"] == 5


def test_a_busy_thread_is_answered_busy_not_delivered(db, slack, cache):
    thread(db)
    cache.claims.add(rt.thread_claim(TENANT, FP))
    assert rt.deliver(state(1)) == "busy"
    assert slack.calls == [] and marker(db) is None
    cache.claims.clear()
    assert rt.deliver(state(1)) == "posted", "llm-server's retry goes in"


def test_a_message_another_writer_is_posting_is_busy_not_posted_twice(db, slack, monkeypatch):
    """The claim fails open when Redis is down: the marker, written before the post, keeps the second writer off."""
    thread(db)
    second = []
    real = slack.reply_in_thread

    def slow(**kw):
        if not second:
            FakeCache.claims.clear()  # Redis gone: the claim lets the second writer in
            second.append(rt.deliver(state(2)))
        return real(**kw)

    monkeypatch.setattr(slack, "reply_in_thread", slow)
    assert rt.deliver(state(1)) == "posted"
    assert second == ["busy"]
    assert len(slack.of("post")) == 1


def test_the_route_answers_busy_with_409(monkeypatch):
    import asyncio

    from notifications_server.routers import common

    monkeypatch.setattr(rt, "deliver", lambda payload: "busy")
    response = asyncio.run(common.post_remediation_thread(state(1)))
    assert response.status_code == 409 and json.loads(response.body) == {"status": "busy"}
    monkeypatch.setattr(rt, "deliver", lambda payload: "stale")
    assert asyncio.run(common.post_remediation_thread(state(1))) == {"status": "stale"}


@pytest.mark.parametrize("error", ["message_not_found", "cant_update_message"])
def test_a_message_slack_lost_is_posted_again_and_its_ts_rewritten(db, slack, error):
    thread(db)
    rt.deliver(state(1))
    slack.fail_update = error
    assert rt.deliver(state(2, options(status="Running"))) == "posted"
    assert [k for k, _ in slack.calls] == ["post", "update", "post"]
    row = marker(db)
    assert row.message_id == "9.3" and json.loads(row.slack_metadata)["state_seq"] == 2


def test_another_edit_refusal_keeps_the_message(db, slack):
    thread(db)
    rt.deliver(state(1))
    slack.fail_update = "ratelimited_forever"
    assert rt.deliver(state(2, options(status="Running"))) == "failed"
    assert len(slack.of("post")) == 1
    assert json.loads(marker(db).slack_metadata)["state_seq"] == 1, "not recorded as shown"


def test_a_replaced_plan_is_only_edited_never_posted(db, slack):
    thread(db)
    replaced = rt.Post(text="_Replaced_", key="options:e:pv-1", edit=True, only_if_posted=True, buttons=[])
    assert rt.deliver(state(1, replaced, options())) == "posted"
    assert len(slack.of("post")) == 1 and marker(db, key="options:e:pv-1") is None


def test_a_state_send_waits_for_the_thread_as_one_row_of_its_newest_state(db, slack):
    assert rt.deliver(state(3)) == "pending"
    assert rt.deliver(state(5, options(status="Running"))) == "pending"
    assert rt.deliver(state(4, options(status="older"))) == "pending"
    with Session(db) as s:
        rows = s.query(SentNotifications).filter(SentNotifications.kind == "remediation_pending").all()
    assert len(rows) == 1 and rt.Posts.model_validate_json(rows[0].slack_metadata).state_seq == 5
    thread(db)
    assert rt.flush_pending(TENANT, FP) == 1
    [post] = slack.of("post")
    assert "Running" in json.dumps(post["blocks"])
    assert rt.flush_pending(TENANT, FP) == 0


def test_a_one_off_report_is_posted_once(db, slack):
    thread(db)
    report = rt.Post(text="### Follow-up check", key="followup:r1:m1")
    assert (
        rt.deliver(rt.Posts(tenant_id=TENANT, account_id=ACCOUNT, event_id=EVENT, fingerprint=FP, posts=[report]))
        == "posted"
    )
    assert (
        rt.deliver(rt.Posts(tenant_id=TENANT, account_id=ACCOUNT, event_id=EVENT, fingerprint=FP, posts=[report]))
        == "already"
    )
    assert len(slack.of("post")) == 1


# --- duplicates -------------------------------------------------------------------------------------------------


def test_the_same_state_goes_into_every_thread_of_the_episode(db, slack):
    thread(db)
    thread(db, fingerprint=DUP_FP, ts=DUP_THREAD)
    assert rt.deliver(state(1)) == "posted"
    assert rt.deliver(state(1, fingerprint=DUP_FP, main=FP)) == "posted"
    posts = slack.of("post")
    assert [p["thread_ts"] for p in posts] == [THREAD, DUP_THREAD]
    assert (
        posts[1]["metadata"]["event_payload"]["main_fingerprint"] == FP
    ), "a click there acts in the main conversation"
    assert marker(db) is not None and marker(db, fingerprint=DUP_FP) is not None, "keyed per thread"
    with Session(db) as s:
        assert rt.episode_main(s, TENANT, DUP_FP) == FP
        assert rt.episode_main(s, TENANT, FP) is None


def test_a_message_in_a_duplicates_thread_points_to_the_main_thread(db, slack):
    thread(db)
    thread(db, fingerprint=DUP_FP, ts=DUP_THREAD)
    rt.deliver(state(1, fingerprint=DUP_FP, main=FP))
    replies = []
    fake = SimpleNamespace(session=Session(db), reply=lambda *a: replies.append(a))
    point = Events._point_to_main_thread.__get__(fake)
    dup = {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": DUP_FP, "loop_on": True}
    assert point(dup, CHANNEL, TEAM, DUP_THREAD) is True
    [(channel, team, ts, message)] = replies
    assert ts == DUP_THREAD and f"https://slack.com/archives/{CHANNEL}/p{THREAD.replace('.', '')}" in message
    assert point({**dup, "fingerprint": FP}, CHANNEL, TEAM, THREAD) is False, "the main thread goes on as before"


# --- clicks -----------------------------------------------------------------------------------------------------


class FakeResponse:
    def __init__(self, status, body=None):
        self.status_code, self._body = status, body or {}

    def json(self):
        return self._body


class EntryCache:
    def __init__(self):
        self.entries = {}

    def cache_event_entry(self, ts, entry):
        self.entries[ts] = entry

    def get_event_entry(self, ts):
        return self.entries.get(ts)


def click_service(engine, slack):
    told, submitted = [], []
    cache = EntryCache()
    common = SimpleNamespace(
        app_id=None,
        post_slack_ephemeral_response=lambda channel, team, user, text_, thread_ts=None: told.append(text_),
        get_slack_installation=lambda team: SimpleNamespace(token="xoxb"),
        update_slack_message_with_blocks=lambda *a: told.append(("edit", a)),
    )
    service = SimpleNamespace(
        engine=engine,
        common_service=common,
        slack_app=SimpleNamespace(client=slack),
        event_service=SimpleNamespace(cache=cache, _submit_followup=lambda *a, **k: submitted.append((a, k))),
    )
    return service, told, submitted, cache


def block_action(kind_value, token=PLAN_TOKEN, action_id="remediation_pick_0", message_ts="9.1", main=FP):
    return {
        "type": "block_actions",
        "api_app_id": "A1",
        "trigger_id": "trig-1",
        "channel": {"id": CHANNEL},
        "team": {"id": TEAM},
        "user": {"id": "U1"},
        "message": {
            "ts": message_ts,
            "thread_ts": THREAD,
            "metadata": {"event_payload": {"token": token, "main_fingerprint": main}},
        },
        "actions": [{"action_id": action_id, "value": json.dumps(kind_value)}],
    }


@pytest.fixture
def http(monkeypatch):
    calls = []
    answers = []

    def post(url, headers=None, json=None, timeout=None):
        calls.append({"url": url, "headers": headers, "json": json})
        return answers.pop(0) if answers else FakeResponse(200)

    monkeypatch.setattr(ra.requests, "post", post)
    return SimpleNamespace(calls=calls, answers=answers)


def test_use_this_fix_is_a_structured_pick_as_the_linked_user(db, slack, http):
    link(db)
    service, told, _, cache = click_service(db, slack)
    http.answers.append(FakeResponse(202))
    ra.handle_block_action(service, block_action({"k": "pick", "o": 1}))
    [chat] = http.calls
    assert chat["url"].endswith(ra.CHAT_PATH)
    assert chat["headers"]["x-user-id"] == USER and chat["headers"]["x-tenant-id"] == TENANT
    body = chat["json"]
    assert body["remediation_pick"] == {"event_id": EVENT, "plan_version": "pv-2", "option": 1, "kind": "run"}
    assert body["session_id"] == "event-" + FP and body["reply_ref"] == f"{CHANNEL}-{THREAD}"
    assert body["user_id"] == USER
    entry = cache.get_event_entry(THREAD)
    assert entry["alert_thread"] and entry["session_id"] == "event-" + FP, "written once the pick is taken"
    assert "asks for approval in this thread" in told[-1]


def test_raise_a_pr_from_a_duplicates_thread_goes_into_the_main_conversation(db, slack, http):
    link(db)
    service, _, _, _ = click_service(db, slack)
    http.answers.append(FakeResponse(202))
    ra.handle_block_action(service, block_action({"k": "pr", "o": 2}, action_id="remediation_pr_1", main=FP))
    body = http.calls[0]["json"]
    assert body["remediation_pick"]["kind"] == "pr" and body["remediation_pick"]["event_id"] == EVENT
    assert body["session_id"] == "event-" + FP


def test_a_refused_pick_is_told_to_the_clicker_and_the_thread_drawn_again(db, slack, http):
    link(db)
    service, told, _, cache = click_service(db, slack)
    http.answers.append(
        FakeResponse(409, {"errors": [{"message": "remediation: these fixes were replaced since you saw them."}]})
    )
    ra.handle_block_action(service, block_action({"k": "pick", "o": 1}))
    assert told == ["Not done: these fixes were replaced since you saw them."]
    assert cache.get_event_entry(THREAD) is None, "no cache entry for a pick that was not taken"
    sync = http.calls[1]
    assert sync["url"].endswith(ra.SYNC_PATH) and sync["json"]["input"]["request"] == {
        "account_id": ACCOUNT,
        "event_id": EVENT,
    }


def test_an_unlinked_clicker_is_told_to_link_and_nothing_is_sent(db, slack, http):
    service, told, _, _ = click_service(db, slack)
    for value, token in (
        ({"k": "pick", "o": 1}, PLAN_TOKEN),
        ({"k": "manual", "o": 3}, PLAN_TOKEN),
        ({"k": "resolve", "r": RUN}, RUN_TOKEN),
    ):
        ra.handle_block_action(service, block_action(value, token=token))
    assert http.calls == []
    assert len(told) == 3 and all("Link your Slack account" in t for t in told)


def test_i_made_this_change_is_the_manual_record(db, slack, http):
    link(db)
    service, told, _, _ = click_service(db, slack)
    ra.handle_block_action(service, block_action({"k": "manual", "o": 3}, action_id="remediation_manual_2"))
    assert http.calls[0]["json"]["input"]["request"] == {
        "account_id": ACCOUNT,
        "event_id": EVENT,
        "step": 2,
        "slot": "manual",
        "plan_version": "pv-2",
        "source": "slack",
    }
    assert "Recorded" in told[0] and http.calls[1]["url"].endswith(ra.SYNC_PATH)


def test_resolve_is_api_servers_resolve_as_the_clicker(db, slack, http):
    link(db)
    service, told, _, _ = click_service(db, slack)
    ra.handle_block_action(
        service, block_action({"k": "resolve", "r": RUN}, token=RUN_TOKEN, action_id="remediation_resolve_0")
    )
    call = http.calls[0]
    assert call["url"].endswith("/rpc/triage")
    assert call["json"] == {
        "action": {"name": "events_apply_remediation_resolve"},
        "input": {"account_id": ACCOUNT, "event_id": EVENT, "run_id": RUN},
        "session_variables": {"tenant_id": TENANT, "user_id": USER},
    }
    assert told == [ra.RESOLVED]


def test_a_stale_resolve_is_answered_politely(db, slack, http):
    link(db)
    service, told, _, _ = click_service(db, slack)
    http.answers.append(FakeResponse(409, {"message": "the alert fired again after the verdict"}))
    ra.handle_block_action(service, block_action({"k": "resolve", "r": RUN}, token=RUN_TOKEN))
    assert told == ["This fix can't be confirmed any more: the alert fired again after the verdict"]
    assert http.calls[1]["url"].endswith(ra.SYNC_PATH)


def test_unreadable_buttons_record_nothing(db, slack, http):
    service, told, _, _ = click_service(db, slack)
    ra.handle_block_action(service, block_action({"k": "pick"}))
    ra.handle_block_action(service, block_action({"k": "pick", "o": 1}, token="remediation-plan:x"))
    ra.handle_block_action(service, block_action({"k": "nope"}))
    assert http.calls == [] and told == [ra.CANT_READ] * 3


# --- modals -----------------------------------------------------------------------------------------------------


def test_other_fixes_opens_its_modal_before_anything_else(db, slack, http, monkeypatch):
    service, _, _, _ = click_service(db, slack)
    monkeypatch.setattr(ra, "_linked", lambda *a: pytest.fail("read before the modal opened"))
    ra.handle_block_action(service, block_action({"k": "other"}, action_id="remediation_other_1"))
    [(kind, opened)] = slack.calls
    assert kind == "views_open" and opened["trigger_id"] == "trig-1"
    view = opened["view"]
    assert view["callback_id"] == ra.CALLBACK_REPLAN
    assert [o["value"] for o in view["blocks"][0]["element"]["options"]] == ra.REPLAN_REASONS
    private = json.loads(view["private_metadata"])
    assert private == {"c": CHANNEL, "t": THREAD, "m": "9.1", "team": TEAM, "app": "A1", "token": PLAN_TOKEN, "fp": FP}
    assert http.calls == []


def test_an_expired_trigger_asks_to_press_again(db, slack, monkeypatch):
    service, told, _, _ = click_service(db, slack)

    def expired(**kw):
        raise SlackApiError(
            "expired", SimpleNamespace(status_code=200, headers={}, data={"error": "expired_trigger_id"})
        )

    monkeypatch.setattr(slack, "views_open", expired)
    ra.handle_block_action(service, block_action({"k": "answer", "a": "yes"}, action_id="remediation_answer_yes"))
    assert told == [ra.TRIGGER_EXPIRED]


def view_submission(callback, private, values):
    return {
        "type": "view_submission",
        "api_app_id": "A1",
        "team": {"id": TEAM},
        "user": {"id": "U1"},
        "view": {"callback_id": callback, "private_metadata": json.dumps(private), "state": {"values": values}},
    }


def test_the_replan_modal_asks_for_other_fixes_with_the_reasons_and_note(db, slack, http):
    link(db)
    service, told, _, _ = click_service(db, slack)
    private = {"c": CHANNEL, "t": THREAD, "m": "9.1", "team": TEAM, "token": PLAN_TOKEN}
    values = {
        "reasons": {"reasons": {"type": "checkboxes", "selected_options": [{"value": "Too risky"}]}},
        "note": {"note": {"type": "plain_text_input", "value": "  needs a change window  "}},
    }
    ra.handle_view_submission(service, view_submission(ra.CALLBACK_REPLAN, private, values))
    call = http.calls[0]
    assert call["url"].endswith(ra.PLAN_PATH) and call["headers"]["x-user-id"] == USER
    assert call["json"]["input"]["request"] == {
        "account_id": ACCOUNT,
        "event_id": EVENT,
        "mode": "replan",
        "reasons": ["Too risky"],
        "note": "needs a change window",
    }
    assert told == [ra.REPLAN_SENT.format(brand=ra._brand())]


@pytest.mark.parametrize("answer,sent", [("yes", "yes"), ("no", "no"), ("norecheck", "no")])
def test_an_approval_modal_answers_the_card_it_came_from_with_the_note(db, slack, answer, sent):
    service, told, submitted, cache = click_service(db, slack)
    cache.cache_event_entry(THREAD, {"alert_thread": True, "followup_msg_ts": "9.4", "tenant_id": TENANT})
    private = {"c": CHANNEL, "t": THREAD, "m": "9.4", "team": TEAM, "a": answer}
    values = {"note": {"note": {"type": "plain_text_input", "value": "after the 14:00 freeze"}}}
    ra.handle_view_submission(service, view_submission(ra.CALLBACK_ANSWER, private, values))
    [(args, kwargs)] = submitted
    assert args[1:6] == (CHANNEL, TEAM, THREAD, "U1", sent)
    assert kwargs["reason"] == "after the 14:00 freeze"
    assert "“after the 14:00 freeze”" in kwargs["replacement_message"] and "<@U1>" in kwargs["replacement_message"]


def test_a_modal_on_a_card_that_is_no_longer_waiting_answers_nothing(db, slack):
    service, told, submitted, cache = click_service(db, slack)
    cache.cache_event_entry(THREAD, {"alert_thread": True, "followup_msg_ts": "9.8"})
    private = {"c": CHANNEL, "t": THREAD, "m": "9.4", "team": TEAM, "a": "yes"}
    ra.handle_view_submission(service, view_submission(ra.CALLBACK_ANSWER, private, {}))
    assert submitted == [] and told == [ra.ANSWER_GONE]


def test_the_interactive_router_takes_a_remediation_modal_submit(monkeypatch):
    import asyncio

    from fastapi import HTTPException

    from notifications_server.routers import actions

    handled = []

    class Service:
        def __init__(self, **kw):
            pass

        def execute_action(self, payload, background_tasks):
            handled.append(payload["type"])

        def close(self):
            pass

    def request(payload):
        async def body():
            return json.dumps(payload).encode()

        return SimpleNamespace(body=body, headers={})

    monkeypatch.setattr(actions, "internal_token_valid", lambda headers: True)
    monkeypatch.setattr(actions, "SlackInteractiveActionsService", Service)
    monkeypatch.setattr(actions, "run_in_threadpool", lambda fn, *a: asyncio.sleep(0, fn(*a)))
    ok = asyncio.run(
        actions.handle_slack_interactive_action(request(view_submission(ra.CALLBACK_ANSWER, {}, {})), None)
    )
    assert ok.status_code == 200 and ok.body == b"", "an empty ack closes the modal"
    with pytest.raises(HTTPException) as refused:
        asyncio.run(actions.handle_slack_interactive_action(request(view_submission("someone_elses", {}, {})), None))
    assert refused.value.status_code == 400, "other payloads without actions are refused as before"
    assert handled == ["view_submission"]


def test_remediation_clicks_are_routed_before_the_email_lookup(monkeypatch):
    routed = []
    service = SimpleNamespace(get_user_email=lambda *a: pytest.fail("email looked up first"))
    monkeypatch.setattr(ra, "handle_block_action", lambda svc, data: routed.append("click"))
    monkeypatch.setattr(ra, "handle_view_submission", lambda svc, data: routed.append("submit"))
    SlackInteractiveActionsService.execute_action(
        service, block_action({"k": "other"}, action_id="remediation_other_1")
    )
    SlackInteractiveActionsService.execute_action(service, view_submission(ra.CALLBACK_REPLAN, {}, {}))
    assert routed == ["click", "submit"]


# --- the approval card ------------------------------------------------------------------------------------------


@pytest.mark.parametrize(
    "remediation,labels,answers",
    [
        (None, ["Approve", "Deny"], ["yes", "no"]),
        ({"recheck": True}, ["Approve anyway", "Deny and re-check"], ["yes", "norecheck"]),
    ],
)
def test_a_step_approval_in_an_alert_thread_is_the_approval_card(remediation, labels, answers):
    follow_up = {"question": "Option 1 of the plan: Roll back", "confirmationKey": "run_step:pv-2:1:apply"}
    if remediation:
        follow_up["remediation"] = remediation
    blocks = Events._remediation_approval_card(follow_up, {"alert_thread": True})
    actions = blocks[-1]["elements"]
    assert [a["text"]["text"] for a in actions] == labels
    assert [json.loads(a["value"]) for a in actions] == [{"k": "answer", "a": a} for a in answers]
    assert all(a["action_id"].startswith("remediation_answer_") for a in actions)


def test_every_other_followup_keeps_its_own_options():
    """With the loop off nothing sends a run_step key, so the follow-up is drawn exactly as before."""
    for follow_up, entry in (
        ({"question": "q", "followupOptions": ["yes", "no"]}, {"alert_thread": True}),
        ({"question": "q", "confirmationKey": "kubectl_write"}, {"alert_thread": True}),
        ({"question": "q", "confirmationKey": "run_step:pv:1:apply"}, {"alert_thread": False}),
        ({"question": "q", "confirmationKey": "run_step:pv:1:apply"}, None),
    ):
        assert Events._remediation_approval_card(follow_up, entry) is None


def test_an_answer_without_a_note_sends_no_reason(monkeypatch):
    """The follow-up answer path is unchanged unless a remediation card adds a note."""
    sent = []
    fake = SimpleNamespace(
        build_llm_payload=Events.build_llm_payload,
        _alert_thread_replier=lambda entry, user: USER,
        common_service=SimpleNamespace(
            update_slack_message_with_blocks=lambda *a: True, slack_reply_in_thread_with_context=lambda *a: None
        ),
        cache=SimpleNamespace(remove_event_keys=lambda *a: None),
        _attach_images=lambda payload, ts: None,
        query_llm_server=lambda payload, headers: sent.append(payload),
        reply=lambda *a: None,
    )
    monkeypatch.setattr("notifications_server.services.events.slack_progress.start_progress_poller", lambda *a: None)
    entry = {
        "text": "x",
        "account_id": ACCOUNT,
        "user_id": USER,
        "tenant_id": TENANT,
        "session_id": "event-" + FP,
        "channel_id": CHANNEL,
        "alert_thread": True,
        "followup_msg_ts": "9.4",
        "followup_question": "q",
    }
    Events._submit_followup(fake, entry, CHANNEL, TEAM, THREAD, "U1", "yes")
    Events._submit_followup(fake, entry, CHANNEL, TEAM, THREAD, "U1", "no", reason="too risky now")
    assert "reason" not in sent[0]
    assert sent[1]["reason"] == "too risky now" and sent[1]["query"] == "no"


# --- the per-step Run / Decline of before -----------------------------------------------------------------------


def test_an_old_step_button_with_the_loop_on_is_retired_politely(db, monkeypatch):
    monkeypatch.setattr(rt, "decide", lambda *a: pytest.fail("decided"))
    told, edits = [], []
    common = SimpleNamespace(
        app_id="A1",
        update_slack_message_with_blocks=lambda *a: edits.append(a),
        post_slack_ephemeral_response=lambda channel, team, user, text_, thread_ts=None: told.append(text_),
    )
    service = SimpleNamespace(common_service=common, engine=db)
    service.handle_remediation_approval = SlackInteractiveActionsService.handle_remediation_approval.__get__(service)
    message = {
        "ts": "2.1",
        "thread_ts": THREAD,
        "metadata": {"event_payload": {"token": f"remediation:{TENANT}:{ACCOUNT}:{EVENT}:pv-1:0"}},
        "blocks": [{"type": "section"}, {"type": "actions"}],
    }
    out = SlackInteractiveActionsService.handle_approval_action(
        service, CHANNEL, TEAM, "U1", None, {"message": message}, {"status": "run"}, None
    )
    assert json.loads(out) == {"status": "retired"}
    assert [b["type"] for b in edits[0][3]] == ["section", "context"], "the buttons go"
    assert "retired" in told[0] and "Use this fix" in told[0]


def test_a_fresh_marker_older_than_the_claim_is_posted_again(db, slack):
    """A marker left without a ts by a writer that died is not busy forever."""
    thread(db)
    with Session(db) as s:
        s.add(
            SentNotifications(
                id=uuid.uuid4(),
                created_at=utc_now() - timedelta(minutes=5),
                tenant_id=uuid.UUID(TENANT),
                fingerprint=rt.message_marker(FP, "options:e:pv-2"),
                kind="remediation_marker",
                slack_metadata=json.dumps({"state_seq": 1}),
            )
        )
        s.commit()
    assert rt.deliver(state(2)) == "posted"
    assert len(slack.of("post")) == 1 and marker(db).message_id == "9.1"


def test_a_state_refused_busy_is_drawn_by_the_holder_before_it_lets_go(db, slack, monkeypatch):
    """Review of PR 2: the newest state arrives while another send holds the thread and llm-server's retries give up
    (the claim can be held past them). The holder draws it before it lets go: the thread ends on the newest state."""
    thread(db)
    answers = []
    real = slack.reply_in_thread

    def slow(**kw):
        if not answers:
            answers.append(rt.deliver(state(2, options(status="Running", buttons=[]))))  # the run-finished sync
        return real(**kw)

    monkeypatch.setattr(slack, "reply_in_thread", slow)
    assert rt.deliver(state(1)) == "posted"
    assert answers == ["busy"], "refused while the first send held the claim, and never retried"
    [update] = slack.of("update")
    assert "Running" in json.dumps(update["blocks"]), "the newest state is what the thread shows"
    assert json.loads(marker(db).slack_metadata)["state_seq"] == 2
    with Session(db) as s:
        assert s.query(SentNotifications).filter(SentNotifications.kind == "remediation_pending").count() == 0


def test_an_older_state_kept_while_busy_is_dropped_not_drawn(db, slack, monkeypatch):
    thread(db)
    answers = []
    real = slack.reply_in_thread

    def slow(**kw):
        if not answers:
            answers.append(rt.deliver(state(1, options(status="older"))))
        return real(**kw)

    monkeypatch.setattr(slack, "reply_in_thread", slow)
    assert rt.deliver(state(3)) == "posted"
    assert answers == ["busy"]
    assert slack.of("update") == [] and json.loads(marker(db).slack_metadata)["state_seq"] == 3


class _FollowupCommon:
    def __init__(self):
        self.posted = []

    def slack_reply_in_thread_as_blocks(self, channel_id, team_id, thread_ts, blocks):
        self.posted.append(blocks)
        return "9.7"


@pytest.mark.parametrize(
    "entry,marked",
    [
        ({"alert_thread": True, "slack_user_id": "U1"}, False),  # an approval card, or a typed message's question
        ({"slack_user_id": "U1"}, True),  # an "Ask Nubi to Analyse" run: answered on its own conversation, as before
    ],
)
def test_an_alert_thread_followup_is_not_taken_for_an_event_analysis_one(monkeypatch, entry, marked):
    """e2e of PR 2: an answer in an alert's thread lost its reply_ref (the event-analysis path drops it), so
    llm-server recorded a Slack answer as answered on the web."""
    from notifications_server.services import events as events_module

    updates = []
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda ts, **kw: updates.append(kw))
    monkeypatch.setattr(events_module.slack_progress, "stop_progress_stream", lambda *a: None)
    service = Events.__new__(Events)
    service.common_service = _FollowupCommon()
    payload = SimpleNamespace(
        conversation_id="event-" + FP,
        session_id="event-" + FP,
        response=json.dumps(
            {"question": "Approve?", "confirmationKey": "run_step:pv:1:apply", "agent_id": "a", "message_id": "m"}
        ),
    )
    Events.handle_followup_response(service, payload, entry, CHANNEL, THREAD, TEAM)
    assert any(u.get("event_analysis_followup") for u in updates) is marked


def test_an_answer_in_an_alert_thread_keeps_its_reply_ref(monkeypatch):
    sent = []
    fake = SimpleNamespace(
        build_llm_payload=Events.build_llm_payload,
        _alert_thread_replier=lambda entry, user: USER,
        common_service=SimpleNamespace(
            update_slack_message_with_blocks=lambda *a: True, slack_reply_in_thread_with_context=lambda *a: None
        ),
        cache=SimpleNamespace(remove_event_keys=lambda *a: None),
        _attach_images=lambda payload, ts: None,
        query_llm_server=lambda payload, headers: sent.append(payload),
        reply=lambda *a: None,
    )
    monkeypatch.setattr("notifications_server.services.events.slack_progress.start_progress_poller", lambda *a: None)
    entry = {
        "text": "x",
        "account_id": ACCOUNT,
        "user_id": USER,
        "tenant_id": TENANT,
        "session_id": "event-" + FP,
        "channel_id": CHANNEL,
        "alert_thread": True,
        "followup_msg_ts": "9.4",
        "followup_question": "q",
    }
    Events._submit_followup(fake, entry, CHANNEL, TEAM, THREAD, "U1", "no", reason="later")
    assert sent[0]["reply_ref"] == f"{CHANNEL}-{THREAD}" and sent[0]["session_id"] == "event-" + FP
