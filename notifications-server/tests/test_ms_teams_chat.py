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
from notifications_server.services.cache import AsyncCache

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


def test_the_welcome_uses_the_app_name_and_the_brand(monkeypatch):
    monkeypatch.setattr(settings.urls, "assistant_name", "Nubi")
    monkeypatch.setattr(settings.urls, "branding_name", "Acme")
    for connected in (True, False):
        card = CommonService.create_teams_welcome_card(connected)
        texts = [b.get("text", "") for b in card["body"]]
        texts += [i["text"] for c in card["body"][0]["columns"] for i in c["items"] if i.get("text")]
        assert "Welcome to Nubi!" in texts
        assert any(t.startswith("Hi there! I'm Nubi, your Acme assistant.") for t in texts)
    connected_text = json.dumps(CommonService.create_teams_welcome_card(True))
    assert "@Nubi" in connected_text and "@Acme" not in connected_text


def test_an_unconnected_chat_is_told_to_connect_a_team_with_no_link():
    card = CommonService.create_teams_welcome_card(False)
    text = json.dumps(card)
    assert "actions" not in card and "/user-management" not in text
    assert "@mention it there" in text and "Sign up" in text


@pytest.mark.parametrize("workspaces, connected", [({"tenant-1"}, True), (set(), False)])
def test_the_welcome_card_follows_the_connection(workspaces, connected):
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    service.teams_reply_with_card = mock.AsyncMock(return_value=True)
    activity = SimpleNamespace(channel_data={"tenant": {"id": "aad-1"}}, conversation=None)
    with mock.patch("notifications_server.services.common.workspaces_for", return_value=workspaces) as workspaces_for:
        asyncio.run(service.teams_send_welcome_message(activity))
    assert workspaces_for.call_args.args[1] is activity
    card = service.teams_reply_with_card.call_args.args[1]
    assert ("Mention" in json.dumps(card)) is connected


# ------------------------------- account picker -------------------------------


def _events():
    events = Events.__new__(Events)
    events.common_service = SimpleNamespace(
        teams_reply=mock.AsyncMock(),
        teams_update_clicked_card=mock.AsyncMock(return_value=True),
        create_teams_account_picked_card=CommonService.create_teams_account_picked_card,
    )
    events.cache = mock.Mock()
    events.cache.aio = AsyncCache(events.cache)
    events.session = mock.Mock()
    events._teams_workspaces = mock.AsyncMock(return_value={NB_TENANT})
    return events


ASKER_CLICK = SimpleNamespace(from_property=SimpleNamespace(aad_object_id="aad-user"))


def test_picking_an_account_turns_the_picker_into_the_account():
    events = _events()
    events.cache.get_event_entry.side_effect = [
        _entry(account_id=None, aad_user_id="aad-user", offered_account_ids=[ACCOUNT]),
        _entry(text=""),
    ]
    account = {"id": ACCOUNT, "account_name": "k8s-dev", "tenant": NB_TENANT}
    with mock.patch("notifications_server.services.events.get_account_by_id", return_value=account):
        asyncio.run(
            events._handle_teams_cluster_selection_and_query_llm(ASKER_CLICK, ACCOUNT, THREAD, "aad", {NB_TENANT})
        )
    card = events.common_service.teams_update_clicked_card.call_args.args[1]
    assert card["body"][0]["text"] == "Working with **k8s-dev**"
    events.common_service.teams_reply.assert_not_called()


def test_the_confirmation_is_sent_when_the_picker_cant_be_updated():
    events = _events()
    events.common_service.teams_update_clicked_card.return_value = False
    events.cache.get_event_entry.side_effect = [
        _entry(account_id=None, aad_user_id="aad-user", offered_account_ids=[ACCOUNT]),
        _entry(text=""),
    ]
    account = {"id": ACCOUNT, "account_name": "k8s-dev", "tenant": NB_TENANT}
    with mock.patch("notifications_server.services.events.get_account_by_id", return_value=account):
        asyncio.run(
            events._handle_teams_cluster_selection_and_query_llm(ASKER_CLICK, ACCOUNT, THREAD, "aad", {NB_TENANT})
        )
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
                "aad-user", SimpleNamespace(), "why?", THREAD, "1800", "aad", THREAD, "dana@example.com", {NB_TENANT}
            )
        )
    assert events._resolve_mapped_account_async.call_args.args[:2] == ("ms_teams", "19:ops")


OTHER_NB_TENANT = "bbbbbbbb-2222-4222-8222-222222222222"


def _ask(events, user_tenants, workspaces):
    events._teams_alert_of_thread = mock.Mock(return_value=None)
    events._resolve_mapped_account_async = mock.AsyncMock(return_value=None)
    events._get_valid_accounts_async = mock.AsyncMock(return_value=[])
    events._extract_conversation_reference = mock.Mock(return_value=REF)
    events.common_service.teams_reply_with_card = mock.AsyncMock()
    with mock.patch(
        "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", user_tenants)
    ):
        asyncio.run(
            events._request_account_confirmation_and_cache_event(
                "aad-user", SimpleNamespace(), "why?", THREAD, "1800", "aad", THREAD, "dana@example.com", workspaces
            )
        )


def test_the_picker_offers_only_the_conversations_workspaces():
    events = _events()
    _ask(events, [OTHER_NB_TENANT, NB_TENANT], {NB_TENANT})
    assert events._resolve_mapped_account_async.call_args.args[3] == [NB_TENANT]
    assert events._get_valid_accounts_async.call_args.args[1] == [NB_TENANT]


def test_a_chat_offers_every_eligible_workspace_of_the_asker():
    events = _events()
    third = "dddddddd-4444-4444-8444-444444444444"
    _ask(events, [OTHER_NB_TENANT, NB_TENANT], {NB_TENANT, OTHER_NB_TENANT, third})
    assert events._get_valid_accounts_async.call_args.args[1] == [OTHER_NB_TENANT, NB_TENANT]


@pytest.mark.parametrize("workspaces", [{OTHER_NB_TENANT}, set()])
def test_a_user_outside_the_connected_workspace_gets_nothing(workspaces):
    events = _events()
    _ask(events, [NB_TENANT], workspaces)
    events._get_valid_accounts_async.assert_not_called()
    events._resolve_mapped_account_async.assert_not_called()
    events.common_service.teams_reply.assert_called_once()


def _message_events(cached):
    events = _events()
    events._extract_activity_info = mock.Mock(return_value=(THREAD, "aad-user", "why?", "1800", "aad"))
    events._is_bot_message = mock.Mock(return_value=False)
    events._is_cluster_selection = mock.Mock(return_value=False)
    events._is_followup_selection = mock.Mock(return_value=False)
    events.common_service.get_teams_user_info = mock.AsyncMock(return_value="dana@example.com")
    events.cache.get_event_entry.return_value = cached
    events._thread_sender_async = mock.AsyncMock(return_value="user-1")
    events._process_teams_message = mock.AsyncMock()
    events._request_account_confirmation_and_cache_event = mock.AsyncMock()
    with mock.patch.object(ca, "is_card_action", return_value=False):
        asyncio.run(events.handle_message_event(SimpleNamespace()))
    return events


def test_a_thread_on_the_connected_workspace_continues():
    events = _message_events(_entry(account_id=ACCOUNT, tenant_id=NB_TENANT))
    events._process_teams_message.assert_called_once()
    events._request_account_confirmation_and_cache_event.assert_not_called()


def test_a_pick_from_another_workspace_is_refused():
    events = _events()
    events.cache.get_event_entry.return_value = _entry(
        account_id=None, aad_user_id="aad-user", offered_account_ids=[ACCOUNT]
    )
    account = {"id": ACCOUNT, "account_name": "elsewhere", "tenant": OTHER_NB_TENANT}
    with mock.patch("notifications_server.services.events.get_account_by_id", return_value=account):
        asyncio.run(
            events._handle_teams_cluster_selection_and_query_llm(ASKER_CLICK, ACCOUNT, THREAD, "aad", {NB_TENANT})
        )
    events.cache.update_event_entry.assert_not_called()
    events.common_service.teams_reply.assert_called_once()


def test_a_followup_on_another_workspace_is_refused():
    events = _events()
    events.cache.get_event_entry.return_value = _entry(tenant_id=OTHER_NB_TENANT)
    events.async_query_llm_server = mock.AsyncMock()
    asyncio.run(
        events._handle_teams_followup_selection_and_query_llm(
            SimpleNamespace(), "more", THREAD, "aad", "dana@example.com", {NB_TENANT}
        )
    )
    events.async_query_llm_server.assert_not_called()
    events.common_service.teams_reply.assert_called_once()


def test_the_workspaces_come_from_the_conversation():
    events = _events()
    del events._teams_workspaces
    activity = SimpleNamespace()
    with (
        mock.patch("notifications_server.services.events.workspaces_for", return_value={NB_TENANT}) as workspaces_for,
        mock.patch("notifications_server.services.events.Session"),
    ):
        assert asyncio.run(events._teams_workspaces(activity)) == {NB_TENANT}
    assert workspaces_for.call_args.args[1] is activity


def test_a_message_in_a_team_that_isnt_connected_gets_only_the_not_connected_reply():
    events = _events()
    events._teams_workspaces = mock.AsyncMock(return_value=set())
    events._extract_activity_info = mock.Mock(return_value=(THREAD, "aad-user", "why?", "1800", "aad"))
    events._is_bot_message = mock.Mock(return_value=False)
    events._reply_team_not_connected = mock.AsyncMock()
    events.common_service.get_teams_user_info = mock.AsyncMock()
    activity = SimpleNamespace(conversation=SimpleNamespace(conversation_type="channel"))
    asyncio.run(events.handle_message_event(activity))
    events._reply_team_not_connected.assert_awaited_once_with(activity, "aad")
    events.common_service.get_teams_user_info.assert_not_called()


@pytest.mark.parametrize("tenant_id", [OTHER_NB_TENANT, None])
def test_a_thread_on_another_workspace_starts_over(tenant_id):
    events = _message_events(_entry(account_id=ACCOUNT, tenant_id=tenant_id))
    events._process_teams_message.assert_not_called()
    events._request_account_confirmation_and_cache_event.assert_called_once()


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


def _alert_events(stored):
    events = _events()
    patches = (
        mock.patch.object(ca, "stored_card", return_value=stored),
        mock.patch("notifications_server.services.events.Session"),
    )
    return events, patches


def test_an_alert_thread_gives_its_account_and_card_text():
    card = get_ms_teams_finding_message_template(FINDING, NB_TENANT)
    events, patches = _alert_events({"kind": "finding", "card": card, "account_id": ACCOUNT})
    with patches[0] as stored_card, patches[1]:
        alert = events._teams_alert_of_thread(THREAD, {NB_TENANT})
    assert stored_card.call_args.args == (NB_TENANT, "19:ops", "1700")
    assert alert["account_id"] == ACCOUNT and "Pod crash looping" in alert["context"]


@pytest.mark.parametrize(
    "conversation, stored",
    [
        ("19:ops", {"kind": "finding", "card": {}, "account_id": ACCOUNT}),
        (THREAD, {"kind": "approval", "card": {}, "account_id": ACCOUNT}),
        (THREAD, {"kind": "finding", "card": {}, "account_id": None}),
        (THREAD, None),
    ],
)
def test_no_alert_outside_an_alert_thread_or_without_an_account(conversation, stored):
    events, patches = _alert_events(stored)
    with patches[0], patches[1]:
        assert events._teams_alert_of_thread(conversation, {NB_TENANT}) is None


ALERT = {"tenant_id": NB_TENANT, "account_id": ACCOUNT, "context": "Pod crash looping"}


def _message_in_thread(alert, *, tenants=(NB_TENANT,), access=True, cached=None, text="why is it down?", value=None):
    """handle_message_event for a message, or a pick when value is given, in THREAD."""
    events = _events()
    events._extract_activity_info = mock.Mock(return_value=(THREAD, "aad-user", text, "1800", "aad"))
    events._is_bot_message = mock.Mock(return_value=False)
    events._handle_teams_followup_selection_and_query_llm = mock.AsyncMock()
    events._handle_exit = mock.AsyncMock()
    events.common_service.get_teams_user_info = mock.AsyncMock(return_value="dana@example.com")
    events._teams_alert_of_thread = mock.Mock(return_value=alert)
    events._validate_user_access_to_account_async = mock.AsyncMock(return_value=access)
    events._process_teams_message = mock.AsyncMock()
    events._request_account_confirmation_and_cache_event = mock.AsyncMock()
    events.cache.get_event_entry.return_value = cached
    with (
        mock.patch(
            "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", list(tenants))
        ),
        mock.patch.object(ca, "is_card_action", return_value=False),
    ):
        asyncio.run(events.handle_message_event(SimpleNamespace(value=value)))
    return events


@pytest.mark.parametrize("tenants, access", [((NB_TENANT,), False), (("another-tenant",), True)])
def test_someone_who_cant_access_the_alerts_account_is_told_and_nothing_else_happens(tenants, access):
    events = _message_in_thread(ALERT, tenants=tenants, access=access)
    events.common_service.teams_reply.assert_awaited_once_with(
        mock.ANY, "You don't have access to this alert's account, so I can't discuss it.", "aad"
    )
    events._request_account_confirmation_and_cache_event.assert_not_awaited()


def test_someone_without_access_is_told_even_in_a_thread_someone_else_started():
    events = _message_in_thread(ALERT, access=False, cached=_entry())
    assert "don't have access" in events.common_service.teams_reply.call_args.args[1]
    events._process_teams_message.assert_not_awaited()


def test_a_follow_up_pick_or_exit_in_an_alert_thread_is_held_to_the_same_access_check():
    pick = {"action_type": "select_followup_option", "followup_option": "payments"}
    events = _message_in_thread(ALERT, access=False, cached=_entry(), text="", value=pick)
    events._handle_teams_followup_selection_and_query_llm.assert_not_awaited()
    events = _message_in_thread(ALERT, access=False, cached=_entry(), text="exit")
    events._handle_exit.assert_not_awaited()
    assert "don't have access" in events.common_service.teams_reply.call_args.args[1]
    events = _message_in_thread(ALERT, cached=_entry(), text="", value=pick)
    events._handle_teams_followup_selection_and_query_llm.assert_awaited_once()


def test_a_new_alert_thread_conversation_is_started_with_its_alert():
    events = _message_in_thread(ALERT)
    events.common_service.teams_reply.assert_not_awaited()
    assert events._request_account_confirmation_and_cache_event.call_args.kwargs == {"alert": ALERT}


def test_an_alert_thread_runs_on_the_alerts_account_never_a_mapped_or_picked_one():
    events = _events()
    events._handle_mapped_account_teams = mock.AsyncMock()
    events._resolve_mapped_account_async = mock.AsyncMock()
    events._get_valid_accounts_async = mock.AsyncMock()
    events._extract_conversation_reference = mock.Mock(return_value=REF)
    with mock.patch(
        "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", [NB_TENANT])
    ):
        asyncio.run(
            events._request_account_confirmation_and_cache_event(
                "aad-user",
                SimpleNamespace(),
                "why?",
                THREAD,
                "1800",
                "aad",
                THREAD,
                "dana@example.com",
                {NB_TENANT},
                alert=ALERT,
            )
        )
    assert events._handle_mapped_account_teams.call_args.args[0] == {"id": ACCOUNT}
    events.cache.update_event_entry.assert_any_call(THREAD, alert_context="Pod crash looping")
    events._resolve_mapped_account_async.assert_not_awaited()
    events._get_valid_accounts_async.assert_not_awaited()


def test_the_alert_channel_context_is_sent_on_every_turn():
    events = _events()
    events.async_query_llm_server = mock.AsyncMock()
    with mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)):
        asyncio.run(events._process_teams_message(SimpleNamespace(), _entry(alert_context="Pod crash"), "why?", "aad"))
        first = events.async_query_llm_server.call_args.args[0]
        asyncio.run(
            events._process_teams_message(SimpleNamespace(), _entry(alert_context="Pod crash"), "and now?", "aad")
        )
        second = events.async_query_llm_server.call_args.args[0]
        asyncio.run(events._process_teams_message(SimpleNamespace(), _entry(), "ordinary message", "aad"))
        third = events.async_query_llm_server.call_args.args[0]
    assert first["query"] == "why?"
    assert first["channel_context"] == "Pod crash"
    assert second["query"] == "and now?"
    assert second["channel_context"] == "Pod crash"
    assert third["query"] == "ordinary message"
    assert "channel_context" not in third


def test_teams_alert_channel_context_survives_subsequent_turns(redis):
    cache = cache_module.Cache()
    events = _events()
    events.cache = cache
    events.async_query_llm_server = mock.AsyncMock()

    # Pre-populate cached alert entry as created during thread initialization
    cache.cache_event_entry(THREAD, _entry(alert_context="Pod crash looping"))

    # Turn 1: user asks an initial question in the alert thread
    turn1_activity = SimpleNamespace(
        conversation=SimpleNamespace(id=THREAD),
        from_property=SimpleNamespace(aad_object_id="user-1", role="user"),
        text="What went wrong?",
        id="1701",
        channel_data={"tenant": {"id": "aad-tenant"}},
        value=None,
    )
    events.common_service.get_teams_user_info = mock.AsyncMock(return_value="dana@example.com")
    with mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)):
        asyncio.run(events.handle_message_event(turn1_activity))

    turn1 = events.async_query_llm_server.call_args.args[0]
    assert turn1["query"] == "What went wrong?"
    assert turn1["channel_context"] == "Pod crash looping"

    # Context persistence: verify alert_context survives in cache and is not gated or cleared
    stored_entry = cache.get_event_entry(THREAD)
    assert stored_entry["alert_context"] == "Pod crash looping"
    assert "alert_context_sent" not in stored_entry

    # Turn 2: user asks a subsequent question in the same thread
    turn2_activity = SimpleNamespace(
        conversation=SimpleNamespace(id=THREAD),
        from_property=SimpleNamespace(aad_object_id="user-1", role="user"),
        text="Why is it crashing?",
        id="1702",
        channel_data={"tenant": {"id": "aad-tenant"}},
        value=None,
    )
    with mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)):
        asyncio.run(events.handle_message_event(turn2_activity))

    turn2 = events.async_query_llm_server.call_args.args[0]
    assert turn2["query"] == "Why is it crashing?"
    assert turn2["channel_context"] == "Pod crash looping"

    # Turn 3: user clicks a follow-up option button in the same thread
    followup_activity = SimpleNamespace(
        conversation=SimpleNamespace(id=THREAD),
        from_property=SimpleNamespace(aad_object_id="user-1", role="user"),
        text=None,
        id="1703",
        channel_data={"tenant": {"id": "aad-tenant"}},
        value={"action_type": "select_followup_option", "followup_option": "Check pod events"},
    )
    with mock.patch.object(thinking, "begin", mock.AsyncMock(return_value=False)):
        asyncio.run(events.handle_message_event(followup_activity))

    turn3 = events.async_query_llm_server.call_args.args[0]
    assert turn3["query"] == "Check pod events"
    assert turn3["channel_context"] == "Pod crash looping"


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


def test_an_answer_names_a_chart_instead_of_showing_its_json():
    # #40909: Adaptive Cards cannot draw an nb-chart, so the card names it.
    markdown = 'CPU rose.\n\n```nb-chart\n{"type":"line","title":"node_cpu (UTC)","labels":["10:00"],"series":[]}\n```'
    card = ac.answer_card(markdown, [])
    assert [b["type"] for b in card["body"]] == ["TextBlock", "TextBlock"]
    assert "Chart: node_cpu (UTC)" in card["body"][1]["text"]
    assert "labels" not in str(card)


def test_an_answer_too_big_for_a_card_falls_back_to_text():
    assert Events._teams_answer_card(_entry(), "x" * 30_000) == (None, None)


def test_an_answer_card_carries_signed_feedback_buttons():
    card, answer_id = Events._teams_answer_card(_entry(), "All good")
    feedback = [a for a in card["actions"] if a.get("id", "").startswith(ca.FEEDBACK_ID)]
    params = ca.verify(feedback[0]["data"][ca.FEEDBACK_DATA], ca.FEEDBACK, {NB_TENANT})
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
        mock.patch.object(ca, "_allowed_for", return_value={NB_TENANT}),
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
