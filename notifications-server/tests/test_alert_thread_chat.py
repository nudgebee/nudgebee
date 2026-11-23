"""A message in an alert thread runs on the alert's account, and whoever can't access that account is told so.
With the remediation loop on it joins the alert's own conversation, event-<fingerprint>, without the thread
transcript, answered in the thread; an answer there is sent as the person who gives it. With it off the thread's
own conversation starts on the alert's account, never a channel's or a picked one."""

import json
import uuid
from datetime import datetime
from types import SimpleNamespace

import pytest
from fastapi import HTTPException
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from sqlalchemy.pool import StaticPool

from notifications_server.configs import settings
from notifications_server.models.models import Base, SentNotifications
from notifications_server.routers import llm_callbacks
from notifications_server.services import events as events_mod
from notifications_server.services import remediation_thread
from notifications_server.services.events import Events

TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
OTHER = "dddddddd-4444-4444-8444-444444444444"
USER = "eeeeeeee-5555-4555-8555-555555555555"
TEAM, CHANNEL, THREAD, FP = "T1", "C1", "1700000000.000100", "fp-1"


@pytest.fixture
def db(monkeypatch):
    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    monkeypatch.setattr(remediation_thread, "loop_on", lambda session, tenant: str(tenant) == TENANT)
    return engine


def row(engine, account=ACCOUNT, fingerprint=FP, thread=THREAD, channel=CHANNEL, tenant=TENANT):
    with Session(engine) as s:
        s.add(
            SentNotifications(
                id=uuid.uuid4(),
                created_at=datetime(2026, 10, 19),
                tenant_id=uuid.UUID(tenant),
                account_id=uuid.UUID(account) if account else None,
                fingerprint=fingerprint,
                slack_team_id=TEAM,
                slack_thread_id=thread,
                slack_metadata=json.dumps({"channel": channel}),
            )
        )
        s.commit()


def service(engine):
    return SimpleNamespace(session=Session(engine))


def lookup(engine, channel=CHANNEL, thread=THREAD):
    return Events._alert_of_thread(service(engine), TEAM, channel, thread)


def test_an_alert_thread_is_found(db):
    row(db)
    assert lookup(db) == {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": FP, "loop_on": True}


def test_an_alert_thread_is_found_with_the_loop_off(db):
    row(db, account=OTHER, thread="1.1", tenant=OTHER)
    assert lookup(db, thread="1.1") == {"tenant_id": OTHER, "account_id": OTHER, "fingerprint": FP, "loop_on": False}


def test_other_threads_are_not_alert_threads(db):
    row(db, fingerprint=f"remediation-pending:{FP}:x", thread="2.2")
    row(db, thread="3.3", channel="C_OTHER")
    row(db, account=None, thread="4.4")
    assert lookup(db, thread="2.2") is None, "one of our own remediation rows is not an alert"
    assert lookup(db, thread="3.3") is None, "the same ts in another channel"
    assert lookup(db, thread="4.4") is None, "an alert with no account recorded has no account to run on"
    assert lookup(db, thread="9.9") is None


class FakeCache:
    def __init__(self, entry=None):
        self.entries = {THREAD: entry} if entry else {}

    def get_event_entry(self, ts):
        return self.entries.get(ts)

    def cache_event_entry(self, ts, entry):
        self.entries[ts] = entry

    def update_event_entry(self, ts, **kwargs):
        self.entries[ts].update(kwargs)

    def remove_event_entry(self, ts):
        self.entries.pop(ts, None)


def alert_service(monkeypatch, *, access=True, cached=None):
    calls = SimpleNamespace(processed=[], followups=[], errors=[])
    monkeypatch.setattr(events_mod, "validate_and_get_user_tenants", lambda email: (USER, [TENANT]))
    fake = SimpleNamespace(
        cache=FakeCache(cached),
        common_service=SimpleNamespace(app_id="A1"),
        _validate_user_access_to_account=lambda u, t, a: access,
        _has_pending_text_followup=lambda entry: bool(entry.get("followup_msg_ts")),
        _submit_followup=lambda *a: calls.followups.append(a),
        _process_event=lambda *a: calls.processed.append(a),
        _reply_error=lambda *a: calls.errors.append(a),
        _refuse_thread_sender=lambda channel, team, thread, message=None: calls.errors.append(
            (channel, team, thread, message)
        ),
        _point_to_main_thread=lambda *a: False,
    )
    fake._alert_account_user = Events._alert_account_user.__get__(fake)
    handle = Events._handle_alert_thread.__get__(fake)
    return handle, fake, calls


ALERT = {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": FP, "loop_on": True}
ALERT_LOOP_OFF = {**ALERT, "loop_on": False}


def test_a_message_in_an_alert_thread_goes_to_the_alerts_conversation(monkeypatch):
    handle, fake, calls = alert_service(monkeypatch)
    handle(ALERT, CHANNEL, TEAM, THREAD, "why did the first fix not work?", "a@b.c", "U1")
    entry = fake.cache.entries[THREAD]
    assert entry["session_id"] == f"event-{FP}"
    assert entry["account_id"] == ACCOUNT and entry["tenant_id"] == TENANT and entry["user_id"] == USER
    assert entry["alert_thread"] is True
    assert calls.processed == [(CHANNEL, "why did the first fix not work?", TEAM, THREAD, "chat")]


def test_someone_without_access_to_the_account_is_told_and_nothing_is_sent(monkeypatch):
    handle, fake, calls = alert_service(monkeypatch, access=False)
    handle(ALERT, CHANNEL, TEAM, THREAD, "hi", "a@b.c", "U1")
    assert not calls.processed and not fake.cache.entries
    assert calls.errors[0][3] == events_mod.NO_ALERT_ACCOUNT_ACCESS


@pytest.mark.parametrize("tenants, access", [([TENANT], True), ([OTHER], True), ([TENANT], False), ([], True)])
def test_only_someone_who_can_access_the_alerts_account_is_its_user(monkeypatch, tenants, access):
    monkeypatch.setattr(events_mod, "validate_and_get_user_tenants", lambda email: (USER, tenants))
    fake = SimpleNamespace(_validate_user_access_to_account=lambda u, t, a: access and (t, a) == (TENANT, ACCOUNT))
    expected = USER if tenants == [TENANT] and access else None
    assert Events._alert_account_user(fake, ALERT, "a@b.c") == expected


def execute(monkeypatch, alert, *, access=True, cached=None):
    """execute_event for a message in THREAD, with what it hands each path recorded."""
    calls = SimpleNamespace(alert_thread=[], new=[], processed=[], errors=[])
    fake = SimpleNamespace(
        safe_add_reaction=lambda *a: None,
        clean_slack_text=lambda text: text,
        _stash_thread_images=lambda *a: None,
        _stash_thread_mentions=lambda *a: None,
        _alert_of_thread=lambda team, channel, thread: alert,
        _alert_account_user=lambda a, email: USER if access else None,
        _handle_alert_thread=lambda *a: calls.alert_thread.append(a),
        _reply_error=lambda *a: calls.errors.append(a),
        _refuse_thread_sender=lambda channel, team, thread, message=None: calls.errors.append(
            (channel, team, thread, message)
        ),
        cache=FakeCache(cached),
        _has_pending_text_followup=lambda entry: False,
        _process_event=lambda *a: calls.processed.append(a),
        _check_history_for_conversation=lambda *a: False,
        _handle_new_conversation=lambda *a, alert=None: calls.new.append(alert),
    )
    Events.execute_event(fake, TEAM, "Ev1", "EC1", "a@b.c", CHANNEL, THREAD, "1700000001.000200", {"text": "q"}, "U1")
    return calls


def test_with_the_loop_off_a_new_alert_thread_conversation_is_started_with_its_alert(monkeypatch):
    calls = execute(monkeypatch, ALERT_LOOP_OFF)
    assert calls.new == [ALERT_LOOP_OFF] and not calls.alert_thread and not calls.errors


def test_with_the_loop_off_someone_without_access_is_told_even_in_a_thread_someone_else_started(monkeypatch):
    started = {"account_id": ACCOUNT, "tenant_id": TENANT, "user_id": "the-first-asker", "slack_user_id": "U0"}
    calls = execute(monkeypatch, ALERT_LOOP_OFF, access=False, cached=started)
    assert [e[3] for e in calls.errors] == [events_mod.NO_ALERT_ACCOUNT_ACCESS]
    assert not calls.processed and not calls.new


def test_with_the_loop_off_an_unfinished_account_pick_restarts_on_the_alerts_account(monkeypatch):
    calls = execute(monkeypatch, ALERT_LOOP_OFF, cached={"user_id": USER, "text": "q"})
    assert calls.new == [ALERT_LOOP_OFF] and not calls.processed


def click(monkeypatch, *, access):
    """A follow-up button click in THREAD, which has a pending follow-up."""
    submitted, errors = [], []
    fake = SimpleNamespace(
        cache=FakeCache({"followup_msg_ts": "1.2", "account_id": ACCOUNT, "tenant_id": TENANT, "user_id": USER}),
        _alert_of_thread=lambda team, channel, thread: ALERT_LOOP_OFF,
        _alert_account_user=lambda a, email: USER if access and email == "b@c.d" else None,
        _thread_sender=lambda entry, email: USER if access and email == "b@c.d" else None,
        _submit_followup=lambda *a: submitted.append(a),
        _reply_error=lambda *a: errors.append(a),
    )
    action = {"action_id": "select_followup_option--payments"}
    Events.update_followup_for_event(fake, action, CHANNEL, TEAM, "U2", THREAD, "1.2", user_email="b@c.d")
    return submitted, errors


def test_a_follow_up_pick_in_an_alert_thread_is_held_to_the_same_access_check(monkeypatch):
    submitted, errors = click(monkeypatch, access=False)
    assert not submitted and [e[3] for e in errors] == [events_mod.NO_ALERT_ACCOUNT_ACCESS]
    submitted, errors = click(monkeypatch, access=True)
    assert len(submitted) == 1 and not errors


def test_with_the_loop_on_the_alerts_own_conversation_is_joined(monkeypatch):
    calls = execute(monkeypatch, ALERT, access=False)
    assert len(calls.alert_thread) == 1 and not calls.new, "_handle_alert_thread checks access itself"


def test_a_new_alert_thread_conversation_takes_the_alerts_account_not_a_bound_or_picked_one(monkeypatch):
    monkeypatch.setattr(events_mod, "validate_and_get_user_tenants", lambda email: (USER, [TENANT]))
    selected, other = [], []
    cache = FakeCache()
    cache.get_channel_session_mapping = lambda *a: other.append("incident channel session")
    fake = SimpleNamespace(
        cache=cache,
        common_service=SimpleNamespace(app_id="A1"),
        _session_id=Events._session_id,
        _handle_selected_account=lambda account, *a: selected.append(account),
        _request_cluster_confirmation=lambda *a: other.append("account picker"),
    )
    Events._handle_new_conversation(fake, CHANNEL, "q", None, None, TEAM, THREAD, "a@b.c", True, "U1", alert=ALERT)
    assert selected == [{"id": ACCOUNT}] and not other
    assert cache.entries[THREAD]["session_id"] == f"{CHANNEL}-{THREAD}", "the thread's own conversation"


def test_an_answer_to_the_agents_question_resumes_it(monkeypatch):
    pending = {"session_id": f"event-{FP}", "user_id": "someone-else", "followup_msg_ts": "1.2"}
    handle, _, calls = alert_service(monkeypatch, cached=pending)
    handle(ALERT, CHANNEL, TEAM, THREAD, "the payments one", "a@b.c", "U1")
    assert calls.followups and not calls.processed
    assert calls.followups[0][0]["user_id"] == USER, "as whoever answers"


def test_an_earlier_cached_turn_of_another_session_is_replaced(monkeypatch):
    stale = {"session_id": f"{CHANNEL}-{THREAD}", "user_id": USER, "followup_msg_ts": "1.2"}
    handle, fake, calls = alert_service(monkeypatch, cached=stale)
    handle(ALERT, CHANNEL, TEAM, THREAD, "propose a rollback instead", "a@b.c", "U1")
    assert not calls.followups
    assert fake.cache.entries[THREAD]["session_id"] == f"event-{FP}"


def test_the_alert_thread_transcript_is_not_sent(monkeypatch):
    fetched, sent = [], []
    entry = {
        "account_id": ACCOUNT,
        "tenant_id": TENANT,
        "user_id": USER,
        "session_id": f"event-{FP}",
        "is_thread": True,
        "alert_thread": True,
        "channel_id": CHANNEL,
        "text": "q",
    }
    monkeypatch.setattr(events_mod.slack_progress, "start_progress_poller", lambda *a: None)
    fake = SimpleNamespace(
        cache=FakeCache(entry),
        common_service=SimpleNamespace(get_slack_conversation=lambda *a: fetched.append(a) or ("transcript", [])),
        _build_channel_context=lambda *a: (None, None),
        build_llm_payload=Events.build_llm_payload,
        _attach_images=lambda p, t: None,
        query_llm_server=lambda payload, headers: sent.append((payload, headers)),
        reply=lambda *a: None,
    )
    Events._process_event(fake, CHANNEL, "why?", TEAM, THREAD, "chat")
    assert not fetched
    assert sent[0][0]["query"] == "why?"
    assert sent[0][0]["session_id"] == f"event-{FP}"
    assert sent[0][0]["reply_ref"] == f"{CHANNEL}-{THREAD}"
    assert sent[0][1] == {"x-tenant-id": TENANT, "x-user-id": USER}


def test_an_answer_in_the_alerts_conversation_goes_back_to_the_thread_it_was_asked_in():
    payload = llm_callbacks.LLMResponse(
        conversation_id=f"event-{FP}", tenant_id=TENANT, type="final", response="x", reply_ref=f"{CHANNEL}-{THREAD}"
    )
    common = SimpleNamespace(get_channel_and_ts_from_sent_notifications=lambda *a, **k: (None, None, "T1", ACCOUNT))
    assert llm_callbacks._parse_conversation(common, f"event-{FP}", payload)[:2] == (CHANNEL, THREAD)


def submit_service(replier):
    sent, replies = [], []
    entry = {
        "tenant_id": TENANT,
        "user_id": USER,
        "session_id": f"event-{FP}",
        "alert_thread": True,
        "account_id": ACCOUNT,
    }
    fake = SimpleNamespace(
        _alert_thread_replier=lambda e, slack_user: replier,
        reply=lambda *a: replies.append(a),
        build_llm_payload=lambda e, ts, query_override=None: {"session_id": e["session_id"], "user_id": e["user_id"]},
        query_llm_server=lambda payload, headers: sent.append((payload, headers)),
        common_service=SimpleNamespace(slack_reply_in_thread_with_context=lambda *a: None),
        cache=SimpleNamespace(remove_event_keys=lambda *a: None),
        _attach_images=lambda p, t: None,
    )
    return fake, entry, sent, replies


def test_an_answer_in_an_alert_thread_is_sent_as_the_person_who_gives_it(monkeypatch):
    monkeypatch.setattr(events_mod.slack_progress, "start_progress_poller", lambda *a: None)
    fake, entry, sent, _ = submit_service(replier="ffffffff-6666-4666-8666-666666666666")
    Events._submit_followup(fake, entry, CHANNEL, TEAM, THREAD, "U2", "yes")
    assert sent and sent[0][1]["x-user-id"] == "ffffffff-6666-4666-8666-666666666666", "not the thread's first writer"
    assert sent[0][0]["user_id"] == "ffffffff-6666-4666-8666-666666666666"


def test_an_unlinked_replier_in_an_alert_thread_is_told_and_nothing_is_sent():
    fake, entry, sent, replies = submit_service(replier=None)
    Events._submit_followup(fake, entry, CHANNEL, TEAM, THREAD, "U2", "yes")
    assert not sent
    assert "Identity Sync" in replies[0][3]
