"""Nubi's account picker: only the person who asked can pick, and only one of
the accounts the picker offered them, on Slack, Microsoft Teams and Google Chat."""

import asyncio
from types import SimpleNamespace
from unittest import mock

import pytest

from notifications_server.services.actions import CLUSTER_NOT_FOUND
from notifications_server.services.events import Events

TENANT = "aaaaaaaa-1111-4111-8111-111111111111"
OTHER_TENANT = "bbbbbbbb-2222-4222-8222-222222222222"
ACCOUNT_ID = "cccccccc-3333-4333-8333-333333333333"
SECOND_ID = "dddddddd-4444-4444-8444-444444444444"
FOREIGN_ID = "eeeeeeee-5555-4555-8555-555555555555"
ACCOUNT = {"id": ACCOUNT_ID, "account_name": "k8s-dev", "tenant": TENANT}
OFFERED = [{"id": ACCOUNT_ID, "account_name": "k8s-dev"}, {"id": SECOND_ID, "account_name": "k8s-prod"}]


def _events():
    events = Events.__new__(Events)
    events.common_service = mock.Mock()
    events.common_service.teams_reply = mock.AsyncMock()
    events.common_service.teams_reply_with_card = mock.AsyncMock()
    events.common_service.teams_update_clicked_card = mock.AsyncMock(return_value=True)
    events.cache = mock.Mock()
    events.session = mock.Mock()
    return events


@pytest.fixture(autouse=True)
def db():
    with mock.patch("notifications_server.services.events.Session"), mock.patch(
        "notifications_server.services.events.get_account_by_id", return_value=dict(ACCOUNT)
    ) as get_account:
        yield get_account


def _offered_ids_stored_before(events, send):
    """Asserts the offered ids were cached before the picker was sent."""
    stored = []

    def record(*_a, **_k):
        stored.extend(c.kwargs.get("offered_account_ids") for c in events.cache.update_event_entry.call_args_list)

    send.side_effect = record
    return stored


# ------------------------------- shared check -------------------------------


def test_only_an_offered_account_is_pickable(db):
    events = _events()
    entry = {"offered_account_ids": [ACCOUNT_ID, SECOND_ID]}
    assert events._get_offered_account(entry, ACCOUNT_ID) == ACCOUNT
    db.reset_mock()
    assert events._get_offered_account(entry, FOREIGN_ID) is None
    assert events._get_offered_account({}, ACCOUNT_ID) is None
    db.assert_not_called()


# ------------------------------- Slack -------------------------------


def _slack_entry(**extra):
    return {
        "text": "why is checkout slow?",
        "user_id": "user-1",
        "slack_user_id": "U-ASKER",
        "offered_account_ids": [ACCOUNT_ID, SECOND_ID],
        **extra,
    }


def _slack_pick(events, clicker, account_id=ACCOUNT_ID):
    events._process_event = mock.Mock()
    events.update_account_for_event(f"select_cluster_option--{account_id}", "C1", "T1", clicker, "1700.1")


def test_slack_the_picker_remembers_what_it_offered():
    events = _events()
    events._resolve_mapped_account = mock.Mock(return_value=None)
    events._get_valid_accounts = mock.Mock(return_value=OFFERED)
    events.common_service.get_slack_user_display_name.return_value = "Dana"
    stored = _offered_ids_stored_before(events, events.common_service.slack_reply_in_thread_as_blocks)
    with mock.patch(
        "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", [TENANT])
    ):
        events._request_cluster_confirmation("C1", "T1", "1700.1", "dana@example.com", "U-ASKER")
    assert stored == [[ACCOUNT_ID, SECOND_ID]]


def test_slack_someone_else_cant_pick():
    events = _events()
    events.cache.get_event_entry.return_value = _slack_entry()
    _slack_pick(events, "U-OTHER")
    events.common_service.post_slack_ephemeral_response.assert_called_once()
    (channel, team, user, message), kwargs = events.common_service.post_slack_ephemeral_response.call_args
    assert (channel, team, user, kwargs) == ("C1", "T1", "U-OTHER", {"thread_ts": "1700.1"})
    assert "Only <@U-ASKER> can choose" in message
    events.common_service.slack_reply_in_thread_with_context.assert_not_called()
    events.cache.update_event_entry.assert_not_called()
    events._process_event.assert_not_called()


@pytest.mark.parametrize("entry", [_slack_entry(), _slack_entry(offered_account_ids=None)])
def test_slack_the_asker_cant_pick_an_account_they_werent_offered(entry):
    events = _events()
    events.cache.get_event_entry.return_value = entry
    _slack_pick(events, "U-ASKER", FOREIGN_ID)
    events.common_service.slack_reply_in_thread.assert_called_once_with("C1", "T1", "1700.1", CLUSTER_NOT_FOUND)
    events.cache.update_event_entry.assert_not_called()
    events._process_event.assert_not_called()


def test_slack_the_asker_picks_and_the_question_runs():
    events = _events()
    events.cache.get_event_entry.return_value = _slack_entry()
    _slack_pick(events, "U-ASKER")
    events.cache.update_event_entry.assert_called_once_with("1700.1", tenant_id=TENANT, account_id=ACCOUNT_ID)
    events._process_event.assert_called_once()


def test_slack_a_pick_on_an_expired_question_runs_nothing():
    events = _events()
    events.cache.get_event_entry.return_value = None
    events.reply = mock.Mock()
    _slack_pick(events, "U-ASKER")
    events.reply.assert_called_once()
    events._process_event.assert_not_called()


# ------------------------------- Microsoft Teams -------------------------------


def _teams_entry(**extra):
    return {
        "text": "",
        "user_id": "user-1",
        "aad_user_id": "aad-asker",
        "session_id": "s-1",
        "conversation_reference": {"user_name": "Dana"},
        "offered_account_ids": [ACCOUNT_ID, SECOND_ID],
        **extra,
    }


def _teams_pick(events, clicker, account_id=ACCOUNT_ID):
    activity = SimpleNamespace(from_property=SimpleNamespace(aad_object_id=clicker))
    asyncio.run(events._handle_teams_cluster_selection_and_query_llm(activity, account_id, "19:ops", "aad"))


def test_teams_the_picker_remembers_what_it_offered():
    events = _events()
    events._teams_alert_of_thread = mock.Mock(return_value=None)
    events._resolve_mapped_account_async = mock.AsyncMock(return_value=None)
    events._get_valid_accounts_async = mock.AsyncMock(return_value=OFFERED)
    events._extract_conversation_reference = mock.Mock(return_value={"conversation_id": "19:ops"})
    stored = _offered_ids_stored_before(events, events.common_service.teams_reply_with_card)
    with mock.patch(
        "notifications_server.services.events.validate_and_get_user_tenants", return_value=("user-1", [TENANT])
    ):
        asyncio.run(
            events._request_account_confirmation_and_cache_event(
                "aad-asker", SimpleNamespace(), "why?", "19:ops", "1800", "aad", "19:ops", "dana@example.com"
            )
        )
    assert stored == [[ACCOUNT_ID, SECOND_ID]]


@pytest.mark.parametrize("clicker", ["aad-other", None])
def test_teams_someone_else_cant_pick(clicker):
    events = _events()
    events.cache.get_event_entry.return_value = _teams_entry()
    _teams_pick(events, clicker)
    assert "Only Dana can choose" in events.common_service.teams_reply.call_args.args[1]
    events.cache.update_event_entry.assert_not_called()


def test_teams_a_card_value_for_an_account_that_wasnt_offered_is_refused(db):
    db.return_value = {"id": FOREIGN_ID, "account_name": "elsewhere", "tenant": OTHER_TENANT}
    events = _events()
    events.cache.get_event_entry.return_value = _teams_entry()
    _teams_pick(events, "aad-asker", FOREIGN_ID)
    assert events.common_service.teams_reply.call_args.args[1] == CLUSTER_NOT_FOUND
    events.cache.update_event_entry.assert_not_called()


class _Response:
    def __init__(self, body):
        self._body = body

    def raise_for_status(self):
        pass

    async def json(self):
        return self._body

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_a):
        return False


class _ApiServerSaysEveryAccountOfTheTenant:
    """api-server's security context lists every account of the tenant asked
    about, whatever the user's roles there — so it can't say who may pick."""

    def __init__(self, *_a, **_k):
        pass

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_a):
        return False

    def post(self, url, json=None, headers=None):
        body = {"context": {"TenantId": json["tenant_id"], "Roles": [], "AccountIds": [FOREIGN_ID]}}
        return _Response(body)


def test_teams_an_account_api_server_lists_but_the_picker_didnt_offer_is_refused(db):
    db.return_value = {"id": FOREIGN_ID, "account_name": "elsewhere", "tenant": OTHER_TENANT}
    events = _events()
    events.cache.get_event_entry.return_value = _teams_entry()
    with mock.patch(
        "notifications_server.services.events.aiohttp.ClientSession", _ApiServerSaysEveryAccountOfTheTenant
    ):
        _teams_pick(events, "aad-asker", FOREIGN_ID)
    assert events.common_service.teams_reply.call_args.args[1] == CLUSTER_NOT_FOUND
    events.cache.update_event_entry.assert_not_called()


def test_teams_the_asker_picks():
    events = _events()
    events.cache.get_event_entry.return_value = _teams_entry()
    _teams_pick(events, "aad-asker")
    events.cache.update_event_entry.assert_called_once_with("19:ops", tenant_id=TENANT, account_id=ACCOUNT_ID)


# ------------------------------- Google Chat -------------------------------


def _gchat_entry(**extra):
    return {
        "text": "why is checkout slow?",
        "user_id": "user-1",
        "user_email": "dana@example.com",
        "user_display_name": "Dana",
        "session_id": "spaces/S/threads/T",
        "tenant_id": TENANT,
        "offered_account_ids": [ACCOUNT_ID, SECOND_ID],
        **extra,
    }


def _gchat_click(events, clicker_email, account_id=ACCOUNT_ID):
    events._process_gchat_message = mock.AsyncMock()
    event = {
        "type": "CARD_CLICKED",
        "user": {"email": clicker_email},
        "space": {"name": "spaces/S"},
        "message": {"thread": {"name": "spaces/S/threads/T"}},
        "common": {"invokedFunction": "select_account", "parameters": {"account_id": account_id}},
    }
    asyncio.run(events.handle_google_chat_event(event))


def test_gchat_the_picker_remembers_what_it_offered():
    events = _events()
    events._resolve_mapped_account_async = mock.AsyncMock(return_value=None)
    events._get_valid_accounts_async = mock.AsyncMock(return_value=OFFERED)
    events._request_gchat_account_confirmation = mock.Mock()
    stored = _offered_ids_stored_before(events, events._request_gchat_account_confirmation)
    asyncio.run(
        events._handle_gchat_new_conversation("user-1", [TENANT], "spaces/S", "spaces/S/threads/T", TENANT, "why?")
    )
    assert stored == [[ACCOUNT_ID, SECOND_ID]]


def test_gchat_someone_else_cant_pick():
    events = _events()
    events.cache.get_event_entry.return_value = _gchat_entry()
    _gchat_click(events, "sam@example.com")
    assert "Only Dana can choose" in events.common_service.gchat_reply_in_thread.call_args.args[2]
    events.cache.update_event_entry.assert_not_called()
    events._process_gchat_message.assert_not_called()


def test_gchat_an_account_that_wasnt_offered_is_refused():
    events = _events()
    events.cache.get_event_entry.return_value = _gchat_entry()
    _gchat_click(events, "dana@example.com", FOREIGN_ID)
    assert events.common_service.gchat_reply_in_thread.call_args.args[2] == CLUSTER_NOT_FOUND
    events.cache.update_event_entry.assert_not_called()
    events._process_gchat_message.assert_not_called()


def test_gchat_the_asker_picks_whatever_the_email_case():
    events = _events()
    events.cache.get_event_entry.return_value = _gchat_entry()
    _gchat_click(events, "Dana@Example.com")
    events.cache.update_event_entry.assert_called_once_with(
        "spaces/S/threads/T", tenant_id=TENANT, account_id=ACCOUNT_ID
    )
    events._process_gchat_message.assert_awaited_once()
