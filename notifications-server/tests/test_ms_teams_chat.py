"""Teams chat: welcome card, account picker, alert-thread context, the Thinking
card, answer cards and feedback."""

import asyncio
import json
import uuid
from types import SimpleNamespace
from unittest import mock

import pytest

from notifications_server.configs import settings
from notifications_server.message_templates.ms_teams.finding import get_ms_teams_finding_message_template
from notifications_server.services import actions as actions_module
from notifications_server.services import cache as cache_module
from notifications_server.services.common import CommonService
from notifications_server.services.events import Events
from notifications_server.services.ms_teams import answer_card as ac
from notifications_server.services.ms_teams import card_actions as ca
from notifications_server.services.ms_teams import thinking

NB_TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
ACCOUNT = "cccccccc-3333-4333-8333-333333333333"
SERVICE_URL = "https://smba.example.com/teams/"
THREAD = "19:ops;messageid=1700"
REF = {"service_url": SERVICE_URL, "conversation_id": THREAD}
FINDING = {
    "id": "event-1",
    "title": "Pod crash looping",
    "created_at": "2026-10-07T10:00:00",
    "fingerprint": "fp-1",
    "aggregation_key": "KubePodCrashLooping",
    "cloud_account_id": ACCOUNT,
}


@pytest.fixture(autouse=True)
def signing_key(monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "test-signing-key")


class FakeRedis:
    """The few Redis calls the cache's chat methods make."""

    def __init__(self):
        self.data = {}

    def get(self, key):
        return self.data.get(key)

    def set(self, key, value, nx=False, ex=None):
        if nx and key in self.data:
            return None
        self.data[key] = value
        return True

    def delete(self, key):
        self.data.pop(key, None)

    def expire(self, key, seconds):
        pass

    def pipeline(self):
        return _FakePipeline(self)


class _FakePipeline:
    def __init__(self, redis):
        self.redis, self.queued = redis, None

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def watch(self, key):
        pass

    def unwatch(self):
        pass

    def multi(self):
        self.queued = []

    def get(self, key):
        return self.redis.get(key)

    def set(self, *args, **kwargs):
        self._do("set", args, kwargs)

    def delete(self, *args):
        self._do("delete", args, {})

    def expire(self, *args):
        pass

    def _do(self, name, args, kwargs):
        if self.queued is None:
            getattr(self.redis, name)(*args, **kwargs)
        else:
            self.queued.append((name, args, kwargs))

    def execute(self):
        for name, args, kwargs in self.queued or []:
            getattr(self.redis, name)(*args, **kwargs)


@pytest.fixture
def redis(monkeypatch):
    fake = FakeRedis()
    monkeypatch.setattr(cache_module.Cache, "__init__", lambda self: setattr(self, "redis_client", fake))
    return fake


def _entry(**extra):
    return {
        "conversation_id": THREAD,
        "account_id": ACCOUNT,
        "tenant_id": NB_TENANT,
        "user_id": "user-1",
        "session_id": f"{THREAD}-_-1800",
        "conversation_reference": REF,
        **extra,
    }


# ------------------------------- welcome -------------------------------


def _action_titles(card):
    return [a["title"] for a in card["actions"]]


def test_a_connected_tenant_is_welcomed_without_sign_up():
    card = CommonService.create_teams_welcome_card(True)
    text = json.dumps(card)
    assert "Sign up" not in text and "Sign Up" not in text
    assert "Mention" in text


def test_an_unconnected_tenant_is_asked_to_connect():
    card = CommonService.create_teams_welcome_card(False)
    assert _action_titles(card) == ["Connect Microsoft Teams"]
    assert card["actions"][0]["url"].endswith("/user-management#integrations")
    assert "Sign up" in json.dumps(card)


@pytest.mark.parametrize("owner, connected", [("tenant-1", True), (None, False)])
def test_the_welcome_card_follows_the_connection(owner, connected):
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    service.teams_reply_with_card = mock.AsyncMock(return_value=True)
    activity = SimpleNamespace(channel_data={"tenant": {"id": "aad-1"}})
    with mock.patch("notifications_server.services.common.ms_teams_aad_tenant_owner", return_value=owner) as owner_of:
        asyncio.run(service.teams_send_welcome_message(activity))
    assert owner_of.call_args.args[1] == "aad-1" and owner_of.call_args.kwargs == {"enabled_only": True}
    card = service.teams_reply_with_card.call_args.args[1]
    assert ("Connect Microsoft Teams" in _action_titles(card)) is not connected


# ------------------------------- account picker -------------------------------


def _events():
    events = Events.__new__(Events)
    events.common_service = SimpleNamespace(
        teams_reply=mock.AsyncMock(),
        teams_update_clicked_card=mock.AsyncMock(return_value=True),
        create_teams_account_picked_card=CommonService.create_teams_account_picked_card,
    )
    events.cache = mock.Mock()
    events.session = mock.Mock()
    return events


def test_picking_an_account_turns_the_picker_into_the_account():
    events = _events()
    events.cache.get_event_entry.side_effect = [_entry(account_id=None), _entry(text="")]
    account = {"id": ACCOUNT, "account_name": "k8s-dev", "tenant": NB_TENANT}
    with mock.patch("notifications_server.services.events.get_account_by_id", return_value=account):
        asyncio.run(events._handle_teams_cluster_selection_and_query_llm(SimpleNamespace(), ACCOUNT, THREAD, "aad"))
    card = events.common_service.teams_update_clicked_card.call_args.args[1]
    assert card["body"][0]["text"] == "Working with **k8s-dev**"
    events.common_service.teams_reply.assert_not_called()


def test_the_confirmation_is_sent_when_the_picker_cant_be_updated():
    events = _events()
    events.common_service.teams_update_clicked_card.return_value = False
    events.cache.get_event_entry.side_effect = [_entry(account_id=None), _entry(text="")]
    account = {"id": ACCOUNT, "account_name": "k8s-dev", "tenant": NB_TENANT}
    with mock.patch("notifications_server.services.events.get_account_by_id", return_value=account):
        asyncio.run(events._handle_teams_cluster_selection_and_query_llm(SimpleNamespace(), ACCOUNT, THREAD, "aad"))
    assert "k8s-dev" in events.common_service.teams_reply.call_args.args[1]


def test_the_channel_mapping_is_looked_up_without_the_thread_suffix():
    events = _events()
    events._teams_alert_of_thread = mock.Mock(return_value=None)
    events._resolve_mapped_account_async = mock.AsyncMock(return_value=None)
    events._get_valid_accounts_async = mock.AsyncMock(return_value=[])
    events._extract_conversation_reference = mock.Mock(return_value=REF)
    with mock.patch(
        "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", [NB_TENANT])
    ):
        asyncio.run(
            events._request_account_confirmation_and_cache_event(
                "aad-user", SimpleNamespace(), "why?", THREAD, "1800", "aad", THREAD, "dana@example.com"
            )
        )
    assert events._resolve_mapped_account_async.call_args.args[:2] == ("ms_teams", "19:ops")


# ------------------------------- alert threads -------------------------------


def test_thread_root_and_card_text():
    assert ca.thread_root_of(THREAD) == "1700"
    assert ca.thread_root_of("19:ops") is None
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    card = ca.with_analysis_status(ca.with_status(card, "Suppressed by Dana"), "Analyzing…")
    text = ca.card_text(card)
    assert "Pod crash looping" in text
    assert "Suppressed by Dana" not in text and "Analyzing…" not in text
    malformed = {
        "body": [{"type": "FactSet", "facts": ["x", {"title": "Pod", "value": "checkout"}]}, "y", {"items": 3}]
    }
    assert ca.card_text(malformed) == "Pod: checkout"


def _alert_events(stored, owner=NB_TENANT):
    events = _events()
    patches = (
        mock.patch("notifications_server.services.events.ms_teams_aad_tenant_owner", return_value=owner),
        mock.patch.object(ca, "stored_card", return_value=stored),
        mock.patch("notifications_server.services.events.Session"),
    )
    return events, patches


def test_an_alert_thread_gives_its_account_and_card_text():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    events, patches = _alert_events({"kind": "finding", "card": card, "account_id": ACCOUNT})
    with patches[0], patches[1] as stored_card, patches[2]:
        alert = events._teams_alert_of_thread(THREAD, "aad", [NB_TENANT])
    assert stored_card.call_args.args == (NB_TENANT, "19:ops", "1700")
    assert alert["account_id"] == ACCOUNT and "Pod crash looping" in alert["context"]


@pytest.mark.parametrize(
    "conversation, stored, tenants",
    [
        ("19:ops", {"kind": "finding", "card": {}, "account_id": ACCOUNT}, [NB_TENANT]),
        (THREAD, {"kind": "approval", "card": {}, "account_id": ACCOUNT}, [NB_TENANT]),
        (THREAD, {"kind": "finding", "card": {}, "account_id": ACCOUNT}, ["another-tenant"]),
        (THREAD, None, [NB_TENANT]),
    ],
)
def test_no_alert_outside_an_alert_thread_or_the_users_workspace(conversation, stored, tenants):
    events, patches = _alert_events(stored)
    with patches[0], patches[1], patches[2]:
        assert events._teams_alert_of_thread(conversation, "aad", tenants) is None


def test_the_alert_goes_with_the_first_question_only():
    events = _events()
    events.async_query_llm_server = mock.AsyncMock()
    with mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)):
        asyncio.run(events._process_teams_message(SimpleNamespace(), _entry(alert_context="Pod crash"), "why?", "aad"))
        first = events.async_query_llm_server.call_args.args[0]["query"]
        events.cache.update_event_entry.assert_called_with(THREAD, alert_context_sent=True)
        asyncio.run(
            events._process_teams_message(
                SimpleNamespace(), _entry(alert_context="Pod crash", alert_context_sent=True), "and now?", "aad"
            )
        )
    assert first.startswith("why?") and "--- context ---" in first and "Pod crash" in first
    assert events.async_query_llm_server.call_args.args[0]["query"] == "and now?"


# ------------------------------- answer cards -------------------------------


def test_an_answer_renders_tables_code_and_headings():
    markdown = "\n".join(
        ["## Pods", "Three are failing:", "", "| Pod | Restarts |", "|---|---:|", "| a \\| b | 3 |", "| c |", ""]
        + ["```", "kubectl get pods", "```"]
    )
    card = ac.answer_card(markdown, [])
    kinds = [b["type"] for b in card["body"]]
    assert kinds == ["TextBlock", "Table", "Container"]
    assert card["body"][0]["text"] == "**Pods**\nThree are failing:"
    rows = card["body"][1]["rows"]
    assert [c["items"][0]["text"] for c in rows[1]["cells"]] == ["a | b", "3"]
    assert [c["items"][0]["text"] for c in rows[2]["cells"]] == ["c", ""], "short rows are padded"
    assert card["body"][2]["items"][0]["inlines"][0]["text"] == "kubectl get pods"
    assert card["version"] == "1.5"


def test_an_answer_too_big_for_a_card_falls_back_to_text():
    assert Events._teams_answer_card(_entry(), "x" * 30_000) == (None, None)


def test_an_answer_card_carries_signed_feedback_buttons():
    card, answer_id = Events._teams_answer_card(_entry(), "All good")
    feedback = [a for a in card["actions"] if a.get("id", "").startswith(ca.FEEDBACK_ID)]
    params = ca.verify(feedback[0]["data"][ca.FEEDBACK_DATA], ca.FEEDBACK, NB_TENANT)
    assert params == {
        "tenant_id": NB_TENANT,
        "account_id": ACCOUNT,
        "answer_id": answer_id,
        "session_id": f"{THREAD}-_-1800",
        "useful": True,
    }


def test_without_the_signing_key_an_answer_card_has_no_feedback(monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "")
    card, answer_id = Events._teams_answer_card(_entry(), "All good")
    assert answer_id is None and [a["type"] for a in card["actions"]] == ["Action.OpenUrl"]


def test_the_answer_replaces_the_thinking_card_and_is_stored():
    events = _events()
    events._store_teams_answer = mock.Mock()
    with mock.patch.object(thinking, "replace", mock.AsyncMock(return_value="2000")) as replace:
        assert asyncio.run(events._deliver_teams_answer(_entry(), THREAD, "All good"))
    activity = replace.call_args.args[2]
    assert activity.attachments[0].content["body"][0]["text"] == "All good"
    assert events._store_teams_answer.call_args.args[2] == "2000"


def test_without_a_thinking_card_the_answer_card_is_sent_new():
    events = _events()
    events._store_teams_answer = mock.Mock()
    sender = mock.Mock()
    sender.send.return_value = "2001"
    with (
        mock.patch.object(thinking, "replace", mock.AsyncMock(return_value=None)),
        mock.patch("notifications_server.services.events.get_sender", return_value=sender),
    ):
        assert asyncio.run(events._deliver_teams_answer(_entry(), THREAD, "All good"))
    assert sender.send.call_args.args[:2] == (SERVICE_URL, THREAD)
    assert events._store_teams_answer.call_args.args[2] == "2001"


def test_a_render_failure_falls_back_to_text():
    events = _events()
    with (
        mock.patch.object(Events, "_teams_answer_card", side_effect=ValueError("bad markdown")),
        mock.patch.object(thinking, "replace", mock.AsyncMock(return_value=None)) as replace,
    ):
        assert not asyncio.run(events._deliver_teams_answer(_entry(), THREAD, "All good"))
    assert replace.call_args.args[2].text == "All good"


def test_with_answer_cards_off_the_text_still_replaces_the_thinking_card(monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "answer_cards_enabled", False)
    events = _events()
    with mock.patch.object(thinking, "replace", mock.AsyncMock(return_value="2000")) as replace:
        assert asyncio.run(events._deliver_teams_answer(_entry(), THREAD, "All good"))
    assert replace.call_args.args[2].text == "All good"


def test_an_answer_card_is_stored_on_the_llm_callback_path(monkeypatch):
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session
    from sqlalchemy.pool import StaticPool

    from notifications_server.models.models import Base, SentNotifications

    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    monkeypatch.setattr(ca, "sync_engine", engine)
    # Built as routers/llm_callbacks.py builds it: no database session.
    events = Events(mock.Mock(), None, None, None)
    entry = _entry(tenant_id=uuid.UUID(NB_TENANT), account_id=uuid.UUID(ACCOUNT))
    card, _ = Events._teams_answer_card(_entry(), "All good")
    card_row = ca.card_row

    def row_with_uuid_id(*args):
        row = card_row(*args)
        row.id = uuid.uuid4()  # the default id is a string, which SQLite rejects
        return row

    with (
        mock.patch.object(thinking, "replace", mock.AsyncMock(return_value="2000")),
        mock.patch.object(Events, "_teams_answer_card", return_value=(card, "ans-1")),
        mock.patch.object(ca, "card_row", side_effect=row_with_uuid_id),
    ):
        assert asyncio.run(events._deliver_teams_answer(entry, THREAD, "All good"))
    with Session(engine) as session:
        row = session.query(SentNotifications).one()
    assert (row.teams_channel_id, row.teams_message_id) == ("19:ops", "2000")
    assert json.loads(row.teams_metadata)["kind"] == "answer"


# ------------------------------- Thinking card -------------------------------


def test_a_turns_thinking_card_is_taken_only_with_its_claim(redis):
    cache = cache_module.Cache()
    cache.cache_event_entry(THREAD, _entry(thinking_id="1900", thinking_claim="turn-2"))
    assert cache.take_teams_thinking(THREAD, "turn-1") is None, "an older turn can't take a newer one's card"
    assert cache.take_teams_thinking(THREAD, "turn-2") == "1900"
    entry = cache.get_event_entry(THREAD)
    assert "thinking_id" not in entry and "thinking_claim" not in entry
    assert cache.take_teams_thinking(THREAD, "turn-2") is None


def test_a_message_lock_is_released_only_by_its_holder(redis):
    cache = cache_module.Cache()
    assert cache.lock_teams_message("1900", "a", 30)
    assert not cache.lock_teams_message("1900", "b", 30)
    cache.unlock_teams_message("1900", "b")
    assert not cache.lock_teams_message("1900", "b", 30)
    cache.unlock_teams_message("1900", "a")
    assert cache.lock_teams_message("1900", "b", 30)


def test_begin_posts_the_card_and_claims_the_turn(redis):
    cache_module.Cache().cache_event_entry(THREAD, _entry())
    sender = mock.Mock()
    sender.send.return_value = "1900"
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert asyncio.run(thinking.begin(THREAD, REF))
    entry = cache_module.Cache().get_event_entry(THREAD)
    assert entry["thinking_id"] == "1900" and entry["thinking_claim"]
    assert sender.send.call_args.args[1] == THREAD


def test_a_new_question_doesnt_take_a_card_another_question_owns(redis):
    import time

    cache_module.Cache().cache_event_entry(
        THREAD, _entry(thinking_id="1900", thinking_claim="turn-1", thinking_at=time.time())
    )
    sender = mock.Mock()
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert not asyncio.run(thinking.begin(THREAD, REF))
    sender.send.assert_not_called()
    assert cache_module.Cache().get_event_entry(THREAD)["thinking_claim"] == "turn-1"


def test_a_stale_card_is_taken_over(redis):
    cache_module.Cache().cache_event_entry(THREAD, _entry(thinking_id="1900", thinking_claim="turn-1", thinking_at=0))
    sender = mock.Mock()
    sender.send.return_value = "1901"
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert asyncio.run(thinking.begin(THREAD, REF))
    assert cache_module.Cache().get_event_entry(THREAD)["thinking_id"] == "1901"


def test_a_failed_request_replaces_only_a_card_it_posted():
    events = _events()
    events.async_query_llm_server = mock.AsyncMock(side_effect=RuntimeError("down"))
    with (
        mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)),
        mock.patch.object(thinking, "replace", mock.AsyncMock(return_value="1900")) as replace,
    ):
        asyncio.run(events._process_teams_message(SimpleNamespace(), _entry(), "why?", "aad"))
    replace.assert_not_called()
    assert events.common_service.teams_reply.called


def test_begin_does_nothing_when_off(redis, monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "thinking_steps_enabled", False)
    sender = mock.Mock()
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert not asyncio.run(thinking.begin(THREAD, REF))
    sender.send.assert_not_called()


def test_replace_updates_the_card_and_clears_the_turn(redis):
    cache_module.Cache().cache_event_entry(THREAD, _entry(thinking_id="1900", thinking_claim="turn-1"))
    sender = mock.Mock()
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert asyncio.run(thinking.replace(THREAD, REF, mock.sentinel.answer)) == "1900"
        assert asyncio.run(thinking.replace(THREAD, REF, mock.sentinel.answer)) is None, "only once"
    assert sender.update.call_args.args == (SERVICE_URL, THREAD, "1900", mock.sentinel.answer)
    assert "teams_message_lock:1900" not in redis.data


def test_replace_gives_up_on_a_held_lock_and_the_answer_goes_new(redis, monkeypatch):
    monkeypatch.setattr(thinking, "LOCK_SECONDS", 0.05)
    monkeypatch.setattr(thinking, "LOCK_WAIT_STEP_SECONDS", 0.01)
    cache = cache_module.Cache()
    cache.cache_event_entry(THREAD, _entry(thinking_id="1900", thinking_claim="turn-1"))
    cache.lock_teams_message("1900", "poller", 30)
    sender = mock.Mock()
    with mock.patch.object(thinking, "get_sender", return_value=sender):
        assert asyncio.run(thinking.replace(THREAD, REF, mock.sentinel.answer)) is None
    sender.update.assert_not_called()
    assert "thinking_claim" not in cache.get_event_entry(THREAD), "the turn is cleared even so"


def _poll_once(redis, delta, before_update=None):
    cache = cache_module.Cache()
    cache.cache_event_entry(THREAD, _entry(thinking_id="1900", thinking_claim="turn-1"))
    entry = cache.get_event_entry(THREAD)
    sender = mock.Mock()
    real_lock = cache_module.Cache.lock_teams_message

    def lock(self, *args):
        if before_update:
            before_update()
        return real_lock(self, *args)

    deltas = iter([delta])

    def fetch(_entry, _since):
        try:
            return next(deltas)
        except StopIteration:
            cache.take_teams_thinking(THREAD, "turn-1")  # the answer arrives
            return {"tool_calls": []}

    with (
        mock.patch.object(thinking, "get_sender", return_value=sender),
        mock.patch.object(thinking, "_fetch_delta", side_effect=fetch),
        mock.patch.object(cache_module.Cache, "lock_teams_message", lock),
        mock.patch.object(thinking.time, "sleep"),
    ):
        thinking._poll(entry, THREAD)
    return sender


STEP = {"tool_calls": [{"id": "t1", "tool_name": "get_pods", "status": "IN_PROGRESS", "updated_at": "1"}]}


def test_the_poller_shows_steps_until_the_answer_takes_the_card(redis):
    sender = _poll_once(redis, STEP)
    assert sender.update.call_count == 1
    card = sender.update.call_args.args[3].attachments[0].content
    assert card["body"][-1]["text"].startswith("… ")


def test_the_poller_never_writes_after_the_answer_took_the_card(redis):
    # The answer lands between the poller's fetch and its update.
    sender = _poll_once(redis, STEP, before_update=lambda: cache_module.Cache().take_teams_thinking(THREAD, "turn-1"))
    sender.update.assert_not_called()


def test_the_poller_skips_unchanged_steps(redis):
    sender = _poll_once(redis, {"tool_calls": []})
    sender.update.assert_not_called()


# ------------------------------- feedback -------------------------------


def _feedback_click(useful=True, params=None):
    params = params or {"tenant_id": NB_TENANT, "account_id": ACCOUNT, "answer_id": "ans-1", "session_id": "s-1"}
    return SimpleNamespace(
        value={ca.ACTION_KEY: ca.FEEDBACK, ca.FEEDBACK_DATA: ca.sign(ca.FEEDBACK, {**params, "useful": useful})},
        reply_to_id="2000",
        service_url=SERVICE_URL,
        conversation=SimpleNamespace(id=THREAD),
        from_property=SimpleNamespace(name="Dana"),
    )


@pytest.fixture
def answer_store(monkeypatch):
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session
    from sqlalchemy.pool import StaticPool

    from notifications_server.models.models import Base, SentNotifications

    engine = create_engine("sqlite:///:memory:", connect_args={"check_same_thread": False}, poolclass=StaticPool)
    Base.metadata.create_all(engine, tables=[SentNotifications.__table__])
    monkeypatch.setattr(ca, "sync_engine", engine)
    card, _ = Events._teams_answer_card(_entry(), "All good")
    row = ca.card_row(uuid.UUID(NB_TENANT), ACCOUNT, None, THREAD, "2000", card, "answer")
    row.id = uuid.uuid4()
    row.account_id = uuid.UUID(ACCOUNT)
    with Session(engine) as session:
        session.add(row)
        session.commit()
    return engine


def _rate(activity, posted=True):
    common = SimpleNamespace(teams_reply=mock.AsyncMock())
    sender = mock.Mock()
    response = SimpleNamespace(ok=posted, status_code=200 if posted else 400)
    with (
        mock.patch.object(ca, "_tenant_for", return_value=uuid.UUID(NB_TENANT)),
        mock.patch.object(actions_module, "validate_and_get_user_tenants", return_value=("user-1", [NB_TENANT])),
        mock.patch(
            "notifications_server.repositories.user_repository.get_llm_conversation_by_session",
            return_value={"id": "conv-1"},
        ) as conversation,
        mock.patch.object(ca.requests, "post", return_value=response) as post,
        mock.patch.object(ca.Cache, "claim_card_action", return_value=True),
        mock.patch.object(ca.Cache, "release_card_action"),
        mock.patch.object(ca, "get_sender", return_value=sender),
    ):
        asyncio.run(ca.handle(common, activity, "dana@example.com", "aad"))
    return SimpleNamespace(
        replies=[c.args[1] for c in common.teams_reply.call_args_list],
        post=post,
        sender=sender,
        conversation=conversation,
    )


def test_a_rating_is_recorded_per_answer_and_the_card_thanks_the_rater(answer_store):
    result = _rate(_feedback_click(useful=False))
    body = result.post.call_args.kwargs["json"]
    request = body["input"]["request"]
    assert body["action"]["name"] == "ai_feedback_create"
    assert body["session_variables"] == {"tenant_id": NB_TENANT, "user_id": "user-1"}
    assert (request["session_id"], request["conversation_id"], request["cloud_account_id"]) == (
        "ans-1",
        "conv-1",
        ACCOUNT,
    )
    assert request["useful"] is False
    assert result.conversation.call_args.args[1] == "s-1"
    card = result.sender.update.call_args.args[3].attachments[0].content
    assert card["body"][-1]["text"].startswith("👎 Rated by Dana")
    assert not any(a.get("id", "").startswith(ca.FEEDBACK_ID) for a in card["actions"])

    again = _rate(_feedback_click())
    again.post.assert_not_called()
    assert again.replies == [ca.FEEDBACK_ALREADY]


def test_a_refused_rating_can_be_retried(answer_store):
    assert _rate(_feedback_click(), posted=False).replies == [ca.FEEDBACK_FAILED]
    assert _rate(_feedback_click()).post.called


def test_tampered_feedback_is_refused(answer_store):
    activity = _feedback_click()
    activity.value[ca.FEEDBACK_DATA] = activity.value[ca.FEEDBACK_DATA].replace("ans-1", "ans-2")
    result = _rate(activity)
    result.post.assert_not_called()
    assert result.replies and result.replies[0] != ca.FEEDBACK_ALREADY
