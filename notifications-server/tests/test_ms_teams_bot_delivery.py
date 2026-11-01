"""Teams sends go through the Bot Connector, and actions the bot can't do are
refused."""

import asyncio

from types import SimpleNamespace
from unittest import mock

import pytest
from msrest.exceptions import HttpOperationError

from notifications_server.configs import settings
from notifications_server.services import common as common_module
from notifications_server.services import message as message_module
from notifications_server.services.common import CommonService
from notifications_server.services.message import MessageService
from notifications_server.services.ms_teams import bot_sender as bs
from notifications_server.services.ms_teams import identity

SERVICE_URL = "https://smba.example.com/teams/"
TEAM = bs.TeamRef(
    integration_id="int-1",
    tenant_id="nb-tenant",
    aad_group_id="group-1",
    thread_id="19:general@thread.tacv2",
    name="Platform",
    service_url=SERVICE_URL,
)


def _http_error(status, retry_after=None):
    response = SimpleNamespace(status_code=status, headers={"Retry-After": retry_after} if retry_after else {})
    error = HttpOperationError.__new__(HttpOperationError)
    error.response = response
    return error


def _sender_with(connector):
    sender = bs.TeamsBotSender.__new__(bs.TeamsBotSender)
    sender.app_id = "app-1"
    sender._credentials = object()
    sender._channel_cache = {}
    sender._connector = lambda service_url: connector
    return sender


# ------------------------------- sender -------------------------------


def test_connect_asks_only_for_the_signed_in_profile():
    assert settings.ms_teams.scopes == ["User.Read"]


def test_send_returns_the_teams_message_id():
    connector = mock.Mock()
    connector.conversations.send_to_conversation.return_value = SimpleNamespace(id="1700000000001")
    assert (
        _sender_with(connector).send(SERVICE_URL, "19:c@thread.tacv2", bs.card_activity({"type": "AdaptiveCard"}))
        == "1700000000001"
    )


def test_thread_replies_address_the_root_message():
    assert bs.thread_conversation_id("19:c@thread.tacv2", "17000") == "19:c@thread.tacv2;messageid=17000"


def test_throttling_is_retried_then_succeeds():
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = [
        _http_error(429, "1"),
        _http_error(412),
        SimpleNamespace(id="ok"),
    ]
    with mock.patch.object(bs.time, "sleep") as sleep:
        assert _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"})) == "ok"
    assert sleep.call_count == 2
    assert 1 <= sleep.call_args_list[0].args[0] < 2, "Retry-After is honoured"


def test_throttling_that_would_hold_a_thread_too_long_is_not_waited_out():
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = _http_error(429, "20")
    with mock.patch.object(bs.time, "sleep") as sleep, pytest.raises(bs.TeamsBotError) as exc:
        _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))
    assert exc.value.status == 429
    sleep.assert_not_called()


def test_retries_stop_once_the_wait_budget_is_spent(caplog):
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = [
        _http_error(429, "4"),
        _http_error(429, "4"),
        _http_error(429, "4"),
    ]
    with mock.patch.object(bs.time, "sleep") as sleep, pytest.raises(bs.TeamsBotError):
        _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))
    assert sleep.call_count == 2
    assert sum(c.args[0] for c in sleep.call_args_list) <= bs.MAX_RETRY_WAIT_SECONDS
    assert "still returned 429 for a send to c" in caplog.text


def test_a_send_that_may_have_been_delivered_is_not_retried():
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = _http_error(502)
    with pytest.raises(bs.TeamsBotError) as exc:
        _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))
    assert exc.value.status == 502
    assert connector.conversations.send_to_conversation.call_count == 1


def test_reads_retry_gateway_errors():
    connector = mock.Mock()
    connector.conversations.get_conversation_member.side_effect = [
        _http_error(504),
        SimpleNamespace(name="Dana", email="dana@example.com", additional_properties={}),
    ]
    with mock.patch.object(bs.time, "sleep"):
        assert _sender_with(connector).get_member(SERVICE_URL, "19:team", "29:dana")["email"] == "dana@example.com"


@pytest.mark.parametrize("error", [PermissionError("token"), TimeoutError("slow"), ConnectionError("down")])
def test_token_and_network_failures_become_send_failures(error):
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = error
    with pytest.raises(bs.TeamsBotError) as exc:
        _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))
    assert exc.value.status is None


def test_building_the_client_cannot_escape_either():
    sender = _sender_with(None)
    sender._connector = mock.Mock(side_effect=PermissionError("bad secret"))
    with pytest.raises(bs.TeamsBotError):
        sender.send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))


def test_a_refusal_is_not_retried():
    connector = mock.Mock()
    connector.conversations.send_to_conversation.side_effect = _http_error(403)
    with pytest.raises(bs.TeamsBotError) as exc:
        _sender_with(connector).send(SERVICE_URL, "c", bs.card_activity({"type": "AdaptiveCard"}))
    assert exc.value.status == 403
    assert str(exc.value) == bs.CHANNEL_FORBIDDEN, "a refused post says what to check"
    assert connector.conversations.send_to_conversation.call_count == 1


def test_member_email_from_a_real_teams_payload():
    from botbuilder.schema import ChannelAccount

    member = ChannelAccount().deserialize(
        {"id": "29:dana", "name": "Dana", "aadObjectId": "user-1", "userPrincipalName": "dana@example.com"}
    )
    connector = mock.Mock()
    connector.conversations.get_conversation_member.return_value = member
    assert _sender_with(connector).get_member(SERVICE_URL, "19:team", "29:dana")["email"] == "dana@example.com"


def test_member_email_falls_back_to_the_upn():
    connector = mock.Mock()
    connector.conversations.get_conversation_member.return_value = SimpleNamespace(
        name="Dana", email=None, user_principal_name="dana@example.com", additional_properties={}
    )
    assert _sender_with(connector).get_member(SERVICE_URL, "19:team", "29:dana") == {
        "name": "Dana",
        "email": "dana@example.com",
    }


def test_a_team_without_a_thread_id_lists_no_channels():
    team = bs.TeamRef("int-2", "nb", "group-2", "", "No thread", SERVICE_URL)
    assert _sender_with(mock.Mock()).list_channels(team) == []


def test_channel_events_refresh_the_cached_list():
    sender = _sender_with(mock.Mock())
    sender._channel_cache["19:team"] = (float("inf"), [{"id": "19:old", "name": "Old"}])
    activity = SimpleNamespace(
        type="conversationUpdate", channel_data={"eventType": "channelCreated", "team": {"id": "19:team"}}
    )
    with mock.patch.object(bs, "get_sender", return_value=sender):
        bs.forget_changed_channels(activity)
    assert "19:team" not in sender._channel_cache


def test_channels_are_listed_once_and_cached():
    sender = _sender_with(mock.Mock())
    result = SimpleNamespace(
        conversations=[
            SimpleNamespace(id="19:general@thread.tacv2", name=None),
            SimpleNamespace(id="19:ops", name="Ops"),
        ]
    )
    client = mock.Mock()
    client.teams.get_teams_channels.return_value = result
    sender._teams_connector = lambda service_url: client
    first = sender.list_channels(TEAM)
    second = sender.list_channels(TEAM)
    assert first == [{"id": "19:general@thread.tacv2", "name": "General"}, {"id": "19:ops", "name": "Ops"}]
    assert second == first
    assert client.teams.get_teams_channels.call_count == 1


# ------------------------------- alert sends -------------------------------


def _message_service():
    svc = MessageService.__new__(MessageService)
    svc._normalize_teams_channels = MessageService._normalize_teams_channels
    return svc


def _ip(team_id="group-1", channel_id="19:ops"):
    return SimpleNamespace(
        tenant_id="nb-tenant", to_channel={"team_id": team_id, "channels": [{"id": channel_id}]}, team_id="tenant-a"
    )


def test_a_template_goes_to_the_mapped_channel_as_the_bot():
    sender = mock.Mock()
    sender.send.return_value = "17001"
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=TEAM)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
    ):
        response = asyncio.run(
            _message_service().send_teams_template_notification(
                _ip(), lambda _: {"type": "AdaptiveCard"}, {}, None, "t"
            )
        )
    assert response["status"] == "success" and response["message_ts"] == "17001"
    assert response["team_id"] == "group-1" and response["channel_id"] == "19:ops"
    service_url, conversation_id, activity = sender.send.call_args.args
    assert (service_url, conversation_id) == (SERVICE_URL, "19:ops")
    assert activity.attachments[0].content == {"type": "AdaptiveCard"}


def test_a_team_without_the_app_fails_visibly():
    with mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=None)):
        response = asyncio.run(_message_service().send_teams_template_notification(_ip(), lambda _: {}, {}, None, "t"))
    assert response["status"] == "failed" and response["reason"] == bs.APP_NOT_IN_TEAM


def test_a_403_is_reported_and_the_team_is_left_alone():
    sender = mock.Mock()
    sender.send.side_effect = bs.TeamsBotError("Microsoft Teams returned 403", status=403)
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=TEAM)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
    ):
        response = asyncio.run(_message_service().send_teams_template_notification(_ip(), lambda _: {}, {}, None, "t"))
    assert response["status"] == "failed" and "403" in response["reason"]


def test_only_the_first_mapped_channel_gets_the_card():
    sender = mock.Mock()
    sender.send.return_value = "17009"
    ip = SimpleNamespace(
        tenant_id="nb-tenant",
        to_channel={"team_id": "group-1", "channels": [{"id": "19:ops"}, {"id": "19:other"}]},
        team_id="tenant-a",
    )
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=TEAM)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
    ):
        response = asyncio.run(_message_service().send_teams_template_notification(ip, lambda _: {}, {}, None, "t"))
    assert [c.args[1] for c in sender.send.call_args_list] == ["19:ops"]
    assert response["channel_id"] == "19:ops" and response["message_ts"] == "17009"


def test_a_thread_reply_goes_under_the_root_message():
    sender = mock.Mock()
    sender.send.return_value = "17002"
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=TEAM)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
    ):
        [response] = asyncio.run(
            _message_service()._send_teams_threaded_reply(None, _ip(), "group-1", "19:ops", "17001", {"type": "x"})
        )
    assert response["status"] == "success" and response["parent_message_id"] == "17001"
    assert sender.send.call_args.args[1] == "19:ops;messageid=17001"


def test_a_finding_without_a_mapped_channel_is_not_sent():
    ip = SimpleNamespace(tenant_id="nb-tenant", to_channel={"team_id": "group-1", "channels": []}, team_id="t")
    with mock.patch.object(message_module, "get_ms_teams_finding_message_template", return_value={}):
        response, sent = asyncio.run(_message_service()._send_finding_to_teams(None, "nb-tenant", ip, {}, None))
    assert response["status"] == "failed" and response["reason"] == "No Microsoft Teams channel is mapped"
    assert sent is None


# ------------------------------- common service -------------------------------


@pytest.fixture
def svc():
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    return service


def test_the_channel_picker_lists_installed_teams_with_their_channels(svc):
    sender = mock.Mock()
    sender.list_channels.return_value = [{"id": "19:ops", "name": "Ops"}]
    with (
        mock.patch.object(common_module, "load_teams", return_value=[TEAM]),
        mock.patch.object(common_module, "get_sender", return_value=sender),
    ):
        result = svc.get_ms_teams_channels(SimpleNamespace(tenant_id="nb-tenant"))
    assert result == {"data": [{"name": "Platform", "id": "group-1", "channels": [{"id": "19:ops", "name": "Ops"}]}]}


def test_a_team_whose_channels_fail_still_appears(svc):
    sender = mock.Mock()
    sender.list_channels.side_effect = bs.TeamsBotError("Microsoft Teams returned 502", status=502)
    with (
        mock.patch.object(common_module, "load_teams", return_value=[TEAM]),
        mock.patch.object(common_module, "get_sender", return_value=sender),
    ):
        result = svc.get_ms_teams_channels(SimpleNamespace(tenant_id="nb-tenant"))
    assert result["data"][0]["channels"] == []


def test_the_test_send_needs_the_app_in_the_team(svc):
    with mock.patch.object(common_module, "load_teams", return_value=[]):
        result = svc._send_test_ms_teams(SimpleNamespace(tenant_id="nb"), "19:ops", "group-1", "hello", "nb")
    assert result == {"success": False, "platform": "ms_teams", "error": bs.APP_NOT_IN_TEAM}


def test_graph_only_actions_are_refused(svc):
    assert svc._add_teams_reaction(SimpleNamespace(), "c", "m", "like", "group-1", "nb")["error"] == bs.NOT_SUPPORTED


# ------------------------------- identity -------------------------------


def _activity(channel=True):
    channel_data = {"tenant": {"id": "tenant-a"}}
    if channel:
        channel_data["team"] = {"id": "19:team@thread.tacv2"}
    return SimpleNamespace(
        type="message",
        service_url=SERVICE_URL,
        from_property=SimpleNamespace(id="29:dana", name="Dana", aad_object_id="user-1"),
        conversation=SimpleNamespace(id="19:ops;messageid=1", conversation_type="channel" if channel else "personal"),
        channel_data=channel_data,
    )


def test_members_are_looked_up_in_the_team_not_the_thread():
    sender = mock.Mock()
    sender.get_member.return_value = {"name": "Dana", "email": "dana@example.com"}
    with mock.patch.object(identity, "get_sender", return_value=sender):
        member = asyncio.run(identity.lookup_member(_activity()))
    assert member["email"] == "dana@example.com"
    assert sender.get_member.call_args.args == (SERVICE_URL, "19:team@thread.tacv2", "29:dana")


# ------------------------------- storage -------------------------------
def test_connect_stores_no_graph_token():
    from notifications_server.services.ms_teams import ms_teams as ms_teams_module

    service = ms_teams_module.MsTeamsService.__new__(ms_teams_module.MsTeamsService)
    service.session = mock.Mock()
    result = {"id_token_claims": {"tid": "tenant-a", "preferred_username": "admin@example.com"}}
    account = {"home_account_id": "object-1.tenant-a", "local_account_id": "object-1"}
    with (
        mock.patch.object(ms_teams_module, "upsert_messaging_integration") as upsert,
        mock.patch.object(ms_teams_module, "get_user_id_by_email", return_value=None),
        mock.patch.object(ms_teams_module, "create_audit_request"),
    ):
        service.save_teams_installation("app-1", "nb-tenant", result, account, "admin@example.com")
    config = upsert.call_args.kwargs["config"]
    assert not {"access_token", "refresh_token", "token_expires_at"} & set(config), "earlier tokens are left alone"
    assert upsert.call_args.kwargs["name"] == "tenant-a"


def test_load_teams_returns_only_enabled_teams_of_the_tenant():
    import uuid

    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session

    from notifications_server.models.models import Base, Integration, IntegrationConfigValue

    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(engine, tables=[Integration.__table__, IntegrationConfigValue.__table__])
    nb, other = uuid.UUID("aaaaaaaa-1111-4111-8111-111111111111"), uuid.UUID("bbbbbbbb-2222-4222-8222-222222222222")

    def team(tenant, name, status="enabled"):
        integ = Integration(
            id=uuid.uuid4(), tenant_id=tenant, type="ms_teams_team", source="user", name=name, status=status, labels={}
        )
        values = [
            IntegrationConfigValue(id=uuid.uuid4(), integration_id=integ.id, name=k, value=v, is_encrypted=False)
            for k, v in {"team_thread_id": f"19:{name}", "team_name": name.title(), "service_url": SERVICE_URL}.items()
        ]
        return [integ, *values]

    with Session(engine) as session:
        session.add_all(
            team(nb, "group-b") + team(nb, "group-a") + team(nb, "group-off", "disabled") + team(other, "group-x")
        )
        session.commit()
        teams = bs.load_teams(session, nb)
    assert [(t.aad_group_id, t.thread_id, t.name, t.service_url) for t in teams] == [
        ("group-a", "19:group-a", "Group-A", SERVICE_URL),
        ("group-b", "19:group-b", "Group-B", SERVICE_URL),
    ]


def test_a_thread_reply_id_names_one_thread():
    assert bs.thread_conversation_id("19:c@thread.tacv2", "17001") == "19:c@thread.tacv2;messageid=17001"
    assert bs.thread_conversation_id("19:c@thread.tacv2;messageid=1", "17001") == "19:c@thread.tacv2;messageid=17001"


def _picker_svc():
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    return service


def test_teams_dms_and_the_user_list_wait_for_the_bot_people_list():
    service = _picker_svc()
    assert "error" in service.send_direct_message("ms_teams", "nb", "user-1", "hi")
    with mock.patch.object(common_module, "load_installation") as load:
        assert service.list_users("ms_teams", "nb") == {"data": []}
    load.assert_not_called()


def test_a_senders_email_is_looked_up_once_and_cached():
    identity._emails.clear()
    lookup = mock.AsyncMock(return_value={"name": "Dana", "email": "dana@example.com"})
    with mock.patch.object(identity, "lookup_member", lookup):
        assert asyncio.run(identity.sender_email(_activity())) == "dana@example.com"
        assert asyncio.run(identity.sender_email(_activity())) == "dana@example.com"
    lookup.assert_awaited_once()
    identity._emails.clear()


def test_a_failed_or_emailless_lookup_is_not_cached():
    identity._emails.clear()
    lookup = mock.AsyncMock(
        side_effect=[None, {"name": "Dana", "email": ""}, {"name": "Dana", "email": "d@example.com"}]
    )
    with mock.patch.object(identity, "lookup_member", lookup):
        assert asyncio.run(identity.sender_email(_activity())) is None
        assert asyncio.run(identity.sender_email(_activity())) is None
        assert asyncio.run(identity.sender_email(_activity())) == "d@example.com"
    identity._emails.clear()


def test_nubi_identifies_the_sender_through_the_bot(svc):
    with (
        mock.patch.object(
            common_module, "load_ms_teams_installation_by_aad_tenant", return_value=SimpleNamespace(tenant_id="nb")
        ),
        mock.patch.object(common_module, "sender_email", mock.AsyncMock(return_value="dana@example.com")),
    ):
        svc.session.query.return_value.filter.return_value.first.return_value = None
        email = asyncio.run(svc.get_teams_user_info("user-1", "tenant-a", _activity()))
    assert email == "dana@example.com"


def _sender_listing(*listings):
    sender = _sender_with(mock.Mock())
    client = mock.Mock()
    client.teams.get_teams_channels.side_effect = [
        SimpleNamespace(conversations=[SimpleNamespace(id=c, name=c) for c in ids]) for ids in listings
    ]
    sender._teams_connector = lambda service_url: client
    return sender, client


def test_a_channel_of_the_team_passes_the_check_including_a_thread():
    sender, _ = _sender_listing(["19:general@thread.tacv2", "19:ops"])
    sender.check_channel(TEAM, "19:ops")
    sender.check_channel(TEAM, "19:ops;messageid=17001")


def test_a_channel_created_since_the_list_was_cached_is_found_on_a_reread():
    sender, client = _sender_listing(["19:ops"], ["19:ops", "19:new"])
    sender.list_channels(TEAM)
    sender.check_channel(TEAM, "19:new")
    assert client.teams.get_teams_channels.call_count == 2


def test_a_channel_outside_the_team_is_refused():
    sender, _ = _sender_listing(["19:ops"], ["19:ops"])
    with pytest.raises(bs.TeamsBotError) as exc:
        sender.check_channel(TEAM, "19:another-customers-channel")
    assert str(exc.value) == bs.CHANNEL_NOT_IN_TEAM


def test_an_alert_to_a_channel_outside_the_team_is_not_sent():
    sender = mock.Mock()
    sender.check_channel.side_effect = bs.TeamsBotError(bs.CHANNEL_NOT_IN_TEAM)
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=TEAM)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
    ):
        response = asyncio.run(_message_service().send_teams_template_notification(_ip(), lambda _: {}, {}, None, "t"))
    assert response["status"] == "failed" and bs.CHANNEL_NOT_IN_TEAM in response["reason"]
    sender.send.assert_not_called()


def test_a_test_send_to_a_channel_outside_the_team_is_not_sent(svc):
    sender = mock.Mock()
    sender.check_channel.side_effect = bs.TeamsBotError(bs.CHANNEL_NOT_IN_TEAM)
    with (
        mock.patch.object(common_module, "load_teams", return_value=[TEAM]),
        mock.patch.object(common_module, "get_sender", return_value=sender),
    ):
        result = svc._send_test_ms_teams(SimpleNamespace(tenant_id="nb"), "19:elsewhere", "group-1", "hello", "nb")
    assert result == {"success": False, "platform": "ms_teams", "error": bs.CHANNEL_NOT_IN_TEAM}
    sender.send.assert_not_called()


def test_a_failed_alert_keeps_its_reason():
    sender = mock.Mock()
    with (
        mock.patch.object(message_module, "load_team_async", mock.AsyncMock(return_value=None)),
        mock.patch.object(message_module, "get_sender", return_value=sender),
        mock.patch.object(message_module, "get_ms_teams_finding_message_template", return_value={}),
    ):
        response, _ = asyncio.run(_message_service()._send_finding_to_teams(None, "nb-tenant", _ip(), {}, None))
    assert response["status"] == "failed" and response["reason"] == bs.APP_NOT_IN_TEAM


class _CountingServer:
    """A local Bot Connector stand-in answering every request with one status."""

    def __init__(self, status):
        import http.server
        import threading

        requests_seen = self.requests = []

        class Handler(http.server.BaseHTTPRequestHandler):
            def _answer(self):
                requests_seen.append(self.command)
                self.rfile.read(int(self.headers.get("Content-Length") or 0))
                self.send_response(status)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", "2")
                self.end_headers()
                self.wfile.write(b"{}")

            do_GET = do_POST = _answer

            def log_message(self, *args):
                pass

        self.server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.url = f"http://127.0.0.1:{self.server.server_port}/"
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def close(self):
        self.server.shutdown()


def _sender_over_http():
    from msrest.authentication import BasicTokenAuthentication

    sender = bs.TeamsBotSender.__new__(bs.TeamsBotSender)
    sender.app_id = "app-1"
    sender._credentials = BasicTokenAuthentication({"access_token": "test"})
    sender._channel_cache = {}
    return sender


def test_a_send_that_gets_a_gateway_error_reaches_teams_once():
    server = _CountingServer(502)
    try:
        with pytest.raises(bs.TeamsBotError):
            _sender_over_http().send(server.url, "19:ops", bs.card_activity({"type": "AdaptiveCard"}))
    finally:
        server.close()
    assert server.requests == ["POST"]


def test_reads_are_retried_by_us_alone():
    server = _CountingServer(502)
    team = bs.TeamRef("int-1", "nb", "group-1", "19:general@thread.tacv2", "Platform", server.url)
    try:
        with mock.patch.object(bs.time, "sleep"), pytest.raises(bs.TeamsBotError):
            _sender_over_http().list_channels(team)
    finally:
        server.close()
    assert server.requests == ["GET"] * bs.MAX_ATTEMPTS
