"""A message in an alert thread of a tenant with the remediation loop on joins the alert's own conversation
(#40734), event-<fingerprint>, on the alert's account, without the thread transcript, answered in the thread; an
answer there is sent as the person who gives it."""

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
                account_id=uuid.UUID(account),
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
    assert lookup(db) == {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": FP}


def test_other_threads_are_not_alert_threads(db):
    row(db, account=OTHER, thread="1.1", tenant=OTHER)
    row(db, fingerprint=f"remediation-pending:{FP}:x", thread="2.2")
    row(db, thread="3.3", channel="C_OTHER")
    assert lookup(db, thread="1.1") is None, "a tenant with the loop off: today's chat is unchanged"
    assert lookup(db, thread="2.2") is None, "one of our own remediation rows is not an alert"
    assert lookup(db, thread="3.3") is None, "the same ts in another channel"
    assert lookup(db, thread="9.9") is None


class FakeCache:
    def __init__(self, entry=None):
        self.entries = {THREAD: entry} if entry else {}

    def get_event_entry(self, ts):
        return self.entries.get(ts)

    def cache_event_entry(self, ts, entry):
        self.entries[ts] = entry


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
    )
    handle = Events._handle_alert_thread.__get__(fake)
    return handle, fake, calls


ALERT = {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": FP}


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
    assert "don't have access" in calls.errors[0][3]


def test_an_answer_to_the_agents_question_resumes_it(monkeypatch):
    pending = {"session_id": f"event-{FP}", "user_id": "someone-else", "followup_msg_ts": "1.2"}
    handle, _, calls = alert_service(monkeypatch, cached=pending)
    handle(ALERT, CHANNEL, TEAM, THREAD, "the payments one", "a@b.c", "U1")
    assert calls.followups and not calls.processed, "whoever answers; _submit_followup sends it as them"


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
