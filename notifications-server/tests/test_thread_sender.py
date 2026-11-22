"""A message, or an answer to a follow-up question, in a chat thread runs as whoever wrote it, with their own
access, on Slack, Microsoft Teams and Google Chat: not as whoever wrote first. One who can't access the thread's
account is told so, nothing is sent, and a question waiting on an answer keeps waiting."""

import asyncio
from types import SimpleNamespace
from unittest import mock

import pytest

from notifications_server.services import events as events_mod
from notifications_server.services.bot_messages import get_thread_account_not_accessible
from notifications_server.services.cache import AsyncCache
from notifications_server.services.events import Events

TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
OTHER_TENANT = "bbbbbbbb-2222-4222-8222-222222222222"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
ALICE, BOB, CAROL, DAVE = (
    "11111111-0000-4000-8000-000000000001",
    "22222222-0000-4000-8000-000000000002",
    "33333333-0000-4000-8000-000000000003",
    "44444444-0000-4000-8000-000000000004",
)
USERS = {
    "alice@example.com": (ALICE, [TENANT]),
    "bob@example.com": (BOB, [TENANT]),
    "carol@example.com": (CAROL, [TENANT]),
    # Granted the account by the check, but not a member of the thread's tenant.
    "dave@example.com": (DAVE, [OTHER_TENANT]),
}
CAN_ACCESS = {ALICE, BOB, DAVE}
REFUSED = get_thread_account_not_accessible()
PENDING = {"followup_msg_ts": "1.5", "followup_question": "Which cluster?", "agent_id": "ag-1", "message_id": "m-1"}


class FakeCache:
    def __init__(self, entries=None, channel_session=None):
        self.entries = {key: dict(value) for key, value in (entries or {}).items()}
        self.images = {}
        self.channel_session = channel_session

    def get_event_entry(self, key):
        entry = self.entries.get(key)
        return dict(entry) if entry else None

    def update_event_entry(self, key, **fields):
        if key not in self.entries:
            return False
        self.entries[key].update(fields)
        return True

    def cache_event_entry(self, thread_ts, event_entry):
        self.entries[thread_ts] = dict(event_entry)

    def remove_event_entry(self, key):
        self.entries.pop(key, None)

    def remove_event_keys(self, key, keys):
        for name in keys:
            self.entries.get(key, {}).pop(name, None)

    def cache_thread_images(self, key, images):
        self.images[key] = images

    def get_thread_images(self, key):
        return self.images.get(key, [])

    def get_channel_session_mapping(self, channel_id, team_id):
        return self.channel_session

    def cache_thread_mentions(self, key, mentions):
        pass

    def get_thread_mentions(self, key):
        return []


@pytest.fixture(autouse=True)
def people(monkeypatch):
    monkeypatch.setattr(
        events_mod,
        "validate_and_get_user_tenants",
        lambda email: USERS.get(email, (None, None)),
    )


def _events(entries):
    events = Events.__new__(Events)
    events.cache = FakeCache(entries)
    events.cache.aio = AsyncCache(events.cache)
    events.common_service = mock.Mock(app_id="A1")
    events.common_service.teams_reply = mock.AsyncMock()
    events.session = mock.Mock()
    events._teams_workspaces = mock.AsyncMock(return_value={TENANT})
    events._validate_user_access_to_account = lambda user, tenant, account: user in CAN_ACCESS and account == ACCOUNT

    async def access(user, tenant, account):
        return events._validate_user_access_to_account(user, tenant, account)

    events._validate_user_access_to_account_async = access
    return events


def _sent(events, monkeypatch):
    """Every request sent to llm-server, as (x-user-id, payload user_id)."""
    sent = []
    monkeypatch.setattr(
        Events, "query_llm_server", staticmethod(lambda payload, headers: sent.append((headers["x-user-id"], payload)))
    )
    monkeypatch.setattr(events_mod.slack_progress, "start_progress_poller", lambda *a, **k: None)

    async def query(payload, headers):
        sent.append((headers["x-user-id"], payload))

    events.async_query_llm_server = query
    return sent


# ------------------------------- Slack -------------------------------

TEAM, CHANNEL, THREAD = "T1", "C1", "1700000000.000100"


def _slack_entry(**extra):
    return {
        "text": "why is checkout slow?",
        "user_id": ALICE,
        "slack_user_id": "U-ALICE",
        "account_id": ACCOUNT,
        "tenant_id": TENANT,
        "is_thread": True,
        "channel_id": CHANNEL,
        "team_id": TEAM,
        "session_id": f"{CHANNEL}-{THREAD}",
        "platform": "slack",
        **extra,
    }


def _slack(entry, alert=None):
    events = _events({THREAD: entry} if entry else {})
    events._alert_of_thread = lambda *a: alert
    events._stash_thread_images = lambda event, team, ts: events.cache.cache_thread_images(ts, event.get("files", []))
    events._build_channel_context = lambda *a: (None, None)
    events.common_service.get_slack_conversation.return_value = ("the thread so far", [])
    return events


def _slack_message(events, email, slack_user_id, text="and the payments one?", files=None):
    event = {"text": text, "files": files or []}
    events.execute_event(TEAM, "ev-1", None, email, CHANNEL, THREAD, "1700000001.000200", event, slack_user_id)


def _slack_pick(events, email, slack_user_id):
    action = {"action_id": "select_followup_option--prod-eu"}
    events.update_followup_for_event(action, CHANNEL, TEAM, slack_user_id, THREAD, "1.5", user_email=email)


def _replies(events):
    return [c.args[3] for c in events.common_service.slack_reply_in_thread.call_args_list]


def test_each_message_in_a_slack_thread_runs_as_whoever_wrote_it(monkeypatch):
    events = _slack(_slack_entry())
    sent = _sent(events, monkeypatch)
    _slack_message(events, "bob@example.com", "U-BOB")
    _slack_message(events, "alice@example.com", "U-ALICE")
    assert [(header, payload["user_id"]) for header, payload in sent] == [(BOB, BOB), (ALICE, ALICE)]
    assert {payload["session_id"] for _, payload in sent} == {f"{CHANNEL}-{THREAD}"}, "one conversation"


def test_a_second_senders_message_captures_the_first_as_in_progress(monkeypatch):
    """#42744 follow-up: whoever held the thread's slack_user_id just before a
    new sender overwrites it is the best signal for "who's turn is actually
    in progress", captured at the one moment that's known reliably."""
    events = _slack(_slack_entry())
    _sent(events, monkeypatch)
    _slack_message(events, "bob@example.com", "U-BOB")
    entry = events.cache.entries[THREAD]
    assert entry["in_progress_asker"] == "U-ALICE"
    assert entry["slack_user_id"] == "U-BOB"


def test_someone_who_cant_access_a_slack_threads_account_is_told_and_the_question_keeps_waiting(monkeypatch):
    events = _slack(_slack_entry(**PENDING))
    sent = _sent(events, monkeypatch)
    _slack_message(events, "carol@example.com", "U-CAROL", "the prod one")
    assert not sent
    assert _replies(events) == [REFUSED]
    entry = events.cache.entries[THREAD]
    assert entry["user_id"] == ALICE and entry["followup_msg_ts"] == "1.5"


@pytest.mark.parametrize("email", ["stranger@example.com", "dave@example.com"], ids=["no user", "another tenant"])
def test_someone_outside_the_threads_tenant_is_refused_in_a_slack_thread(monkeypatch, email):
    events = _slack(_slack_entry())
    sent = _sent(events, monkeypatch)
    _slack_message(events, email, "U-X")
    assert not sent and _replies(events) == [REFUSED]


@pytest.mark.parametrize(
    "alert, refusal",
    [
        (None, REFUSED),
        (
            {"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": "fp-1", "loop_on": False},
            events_mod.NO_ALERT_ACCOUNT_ACCESS,
        ),
    ],
    ids=["a thread", "an alert's thread"],
)
def test_a_refused_senders_images_dont_go_with_the_next_answer(monkeypatch, alert, refusal):
    events = _slack(_slack_entry(**PENDING), alert=alert)
    sent = _sent(events, monkeypatch)
    _slack_message(events, "carol@example.com", "U-CAROL", "this one", files=[{"name": "carol.png"}])
    assert _replies(events) == [refusal]
    _slack_pick(events, "bob@example.com", "U-BOB")
    assert [header for header, _ in sent] == [BOB]
    assert "images" not in sent[0][1]


def test_a_text_answer_in_an_alert_thread_is_sent_as_whoever_answers(monkeypatch):
    # The entry llm-server's callback writes when the alert's conversation asks before anyone has written here.
    pending = {
        "channel_id": CHANNEL,
        "team_id": TEAM,
        "account_id": ACCOUNT,
        "tenant_id": TENANT,
        "session_id": "event-fp-1",
        "is_thread": False,
        **PENDING,
    }
    events = _slack(pending, alert={"tenant_id": TENANT, "account_id": ACCOUNT, "fingerprint": "fp-1", "loop_on": True})
    sent = _sent(events, monkeypatch)
    _slack_message(events, "bob@example.com", "U-BOB", "the payments one")
    assert [(header, payload["user_id"], payload["session_id"]) for header, payload in sent] == [
        (BOB, BOB, "event-fp-1")
    ]


@pytest.mark.parametrize("email, user", [("bob@example.com", BOB), ("stranger@example.com", None)])
def test_a_new_thread_in_a_bound_channel_runs_as_whoever_wrote_it(monkeypatch, email, user):
    events = _slack(None)
    events.cache.channel_session = {"session_id": "S1"}
    joined = {"account_id": ACCOUNT, "tenant_id": TENANT, "user_id": ALICE, "channel_id": CHANNEL, "team_id": TEAM}
    events.cache.entries["S1"] = joined
    sent = _sent(events, monkeypatch)
    monkeypatch.setattr(events_mod, "get_llm_conversation_by_session", lambda session, session_id: None)
    monkeypatch.setattr(events_mod, "Session", mock.MagicMock())
    _slack_message(events, email, "U-SOMEONE")
    if user:
        assert [(header, payload["session_id"]) for header, payload in sent] == [(BOB, "event-S1")]
    else:
        assert not sent and _replies(events) == [REFUSED]
        assert events.cache.entries["S1"]["user_id"] == ALICE and THREAD not in events.cache.entries


def test_a_slack_follow_up_pick_is_answered_as_whoever_picks(monkeypatch):
    events = _slack(_slack_entry(**PENDING))
    sent = _sent(events, monkeypatch)
    _slack_pick(events, "bob@example.com", "U-BOB")
    assert [(header, payload["user_id"], payload["query"]) for header, payload in sent] == [(BOB, BOB, "prod-eu")]
    entry = events.cache.entries[THREAD]
    assert entry["user_id"] == BOB and entry["slack_user_id"] == "U-BOB"


def test_a_slack_follow_up_pick_by_someone_without_access_is_refused_and_not_taken(monkeypatch):
    events = _slack(_slack_entry(**PENDING))
    sent = _sent(events, monkeypatch)
    _slack_pick(events, "carol@example.com", "U-CAROL")
    assert not sent
    assert events.common_service.slack_reply_in_thread.call_args.args[3] == REFUSED
    events.common_service.update_slack_message_with_blocks.assert_not_called()
    entry = events.cache.entries[THREAD]
    assert entry["user_id"] == ALICE and entry["followup_msg_ts"] == "1.5" and entry["agent_id"] == "ag-1"


@pytest.mark.parametrize("email, resumed", [("bob@example.com", True), ("carol@example.com", False)])
def test_an_expired_slack_thread_resumes_only_for_someone_who_can_access_its_account(monkeypatch, email, resumed):
    events = _slack(None)
    sent = _sent(events, monkeypatch)
    conversation = {"user_id": ALICE, "account_id": ACCOUNT, "tenant_id": TENANT, "session_id": f"{CHANNEL}-{THREAD}"}
    monkeypatch.setattr(events_mod, "get_llm_conversation_by_session", lambda session, session_id: conversation)
    monkeypatch.setattr(events_mod, "Session", mock.MagicMock())
    _slack_message(events, email, "U-SOMEONE")
    if resumed:
        assert [header for header, _ in sent] == [BOB]
        assert events.cache.entries[THREAD]["user_id"] == BOB
    else:
        assert not sent and THREAD not in events.cache.entries
        assert _replies(events) == [REFUSED]


# ------------------------------- Microsoft Teams -------------------------------

CONVERSATION = "19:ops@thread.tacv2;messageid=1700"
AAD = {"aad-alice": "alice@example.com", "aad-bob": "bob@example.com", "aad-carol": "carol@example.com"}


def _teams_entry(**extra):
    return {
        "text": "why is checkout slow?",
        "user_id": ALICE,
        "aad_user_id": "aad-alice",
        "conversation_id": CONVERSATION,
        "account_id": ACCOUNT,
        "tenant_id": TENANT,
        "session_id": f"{CONVERSATION}-_-1700",
        "platform": "ms_teams",
        **extra,
    }


def _teams(entry, monkeypatch):
    events = _events({CONVERSATION: entry})
    events._teams_alert_of_thread = lambda *a: None

    async def email_of(aad_user_id, *_):
        return AAD[aad_user_id]

    events.common_service.get_teams_user_info = email_of

    async def no_thinking_card(*_):
        return False

    monkeypatch.setattr(events_mod.teams_thinking, "begin", no_thinking_card)
    return events


def _teams_activity(aad_user_id, text="", value=None):
    return SimpleNamespace(
        conversation=SimpleNamespace(id=CONVERSATION),
        from_property=SimpleNamespace(aad_object_id=aad_user_id, role="user"),
        text=text,
        id="1800",
        channel_data={"tenant": {"id": "aad-tenant"}},
        value=value,
    )


def _teams_message(events, aad_user_id, text="and the payments one?"):
    asyncio.run(events.handle_message_event(_teams_activity(aad_user_id, text)))


def _teams_pick(events, aad_user_id):
    value = {"action_type": "select_followup_option", "followup_option": "prod-eu"}
    asyncio.run(events.handle_message_event(_teams_activity(aad_user_id, value=value)))


def _teams_replies(events):
    return [c.args[1] for c in events.common_service.teams_reply.call_args_list]


def test_each_message_in_a_teams_thread_runs_as_whoever_wrote_it(monkeypatch):
    events = _teams(_teams_entry(), monkeypatch)
    sent = _sent(events, monkeypatch)
    _teams_message(events, "aad-bob")
    assert events.cache.entries[CONVERSATION]["user_id"] == BOB, "the Thinking card's poller reads it from here"
    _teams_message(events, "aad-alice")
    assert [(header, payload["user_id"]) for header, payload in sent] == [(BOB, BOB), (ALICE, ALICE)]
    assert {payload["session_id"] for _, payload in sent} == {f"{CONVERSATION}-_-1700"}, "one conversation"


def test_someone_who_cant_access_a_teams_threads_account_is_told(monkeypatch):
    events = _teams(_teams_entry(), monkeypatch)
    sent = _sent(events, monkeypatch)
    _teams_message(events, "aad-carol")
    assert not sent and _teams_replies(events) == [REFUSED]
    assert events.cache.entries[CONVERSATION]["user_id"] == ALICE


def test_a_teams_follow_up_pick_is_answered_as_whoever_picks(monkeypatch):
    events = _teams(_teams_entry(**PENDING), monkeypatch)
    sent = _sent(events, monkeypatch)
    _teams_pick(events, "aad-bob")
    assert [(header, payload["user_id"], payload["query"]) for header, payload in sent] == [(BOB, BOB, "prod-eu")]
    assert events.cache.entries[CONVERSATION]["user_id"] == BOB


def test_a_teams_follow_up_pick_by_someone_without_access_is_refused(monkeypatch):
    events = _teams(_teams_entry(**PENDING), monkeypatch)
    sent = _sent(events, monkeypatch)
    _teams_pick(events, "aad-carol")
    assert not sent and _teams_replies(events) == [REFUSED]
    assert events.cache.entries[CONVERSATION]["user_id"] == ALICE


# ------------------------------- Google Chat -------------------------------

SPACE, GCHAT_THREAD = "spaces/S", "spaces/S/threads/T"


def _gchat_entry(**extra):
    return {
        "text": "why is checkout slow?",
        "user_id": ALICE,
        "account_id": ACCOUNT,
        "tenant_id": TENANT,
        "session_id": GCHAT_THREAD,
        **extra,
    }


def _gchat_message(events, email):
    event = {
        "user": {"email": email},
        "space": {"name": SPACE},
        "message": {"thread": {"name": GCHAT_THREAD}, "name": "m-2", "text": "and the payments one?"},
    }
    asyncio.run(events._handle_gchat_message(event))


def _gchat_pick(events, email):
    event = {
        "type": "CARD_CLICKED",
        "user": {"email": email},
        "space": {"name": SPACE},
        "message": {"thread": {"name": GCHAT_THREAD}},
        "common": {"invokedFunction": "select_followup", "parameters": {"option": "prod-eu"}},
    }
    asyncio.run(events.handle_google_chat_event(event))


def _gchat_replies(events):
    return [c.args[2] for c in events.common_service.gchat_reply_in_thread.call_args_list]


@pytest.mark.parametrize("email, user", [("bob@example.com", BOB), ("carol@example.com", None)])
def test_a_google_chat_thread_message_runs_as_whoever_wrote_it(email, user):
    events = _events({GCHAT_THREAD: _gchat_entry()})
    events._process_gchat_message = mock.AsyncMock()
    _gchat_message(events, email)
    if user:
        assert events._process_gchat_message.await_args.args[0]["user_id"] == user
    else:
        events._process_gchat_message.assert_not_awaited()
        assert _gchat_replies(events) == [REFUSED]
        assert events.cache.entries[GCHAT_THREAD]["user_id"] == ALICE


@pytest.mark.parametrize("email, user", [("bob@example.com", BOB), ("carol@example.com", None)])
def test_a_google_chat_follow_up_pick_is_answered_as_whoever_picks(monkeypatch, email, user):
    events = _events({GCHAT_THREAD: _gchat_entry(**PENDING)})
    sent = _sent(events, monkeypatch)
    _gchat_pick(events, email)
    if user:
        assert [(header, payload["user_id"]) for header, payload in sent] == [(BOB, BOB)]
        assert events.cache.entries[GCHAT_THREAD]["user_id"] == BOB
    else:
        assert not sent and _gchat_replies(events) == [REFUSED]
        assert events.cache.entries[GCHAT_THREAD]["user_id"] == ALICE
