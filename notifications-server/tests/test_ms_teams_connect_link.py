"""Connect a Microsoft Teams team from the bot: who gets a link on the welcome
card or for an @mention, what the link carries, and who can redeem it.

Activities and members are shaped like Microsoft's documented samples
(conversationUpdate when a bot is added to a team, a channel message, and the
Bot Connector's get-member response) with neutral values. The member is
deserialized as the Bot Connector does, so its sign-in name, tenant and role
exist only in additional_properties."""

import asyncio
import datetime
import base64
import hashlib
import hmac
import json
import uuid
from types import SimpleNamespace
from unittest import mock

import pytest
from botbuilder.schema import Activity, ChannelAccount, Entity
from msrest.exceptions import HttpOperationError

from notifications_server.configs import settings
from notifications_server.services import common as common_module
from notifications_server.services import events as events_module
from notifications_server.services import messaging_installations as mi
from notifications_server.services.common import CommonService
from notifications_server.services.events import Events
from notifications_server.services.ms_teams import bot_sender as bs
from notifications_server.services.ms_teams import card_actions
from notifications_server.services.ms_teams import connect_link as cl
from notifications_server.services.ms_teams import identity
from notifications_server.services.ms_teams import team_binding as tb
from notifications_server.utils.action_requests import ActionRequestBody, sign_action_request

SERVICE_URL = "https://smba.trafficmanager.net/amer/"
AAD_TENANT = "0d4c1a5e-7b2f-4e8a-9c3d-6f1b2a3c4d5e"
OTHER_AAD_TENANT = "5e4d3c2b-1a0f-4e9d-8c7b-6a5f4e3d2c1b"
TEAM_THREAD = "19:a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6@thread.tacv2"
TEAM_GROUP = "f3ec8cd2-e704-4344-8c47-9a3a21d683c0"
BOT = "28:11111111-2222-4333-8444-555555555555"
INSTALLER = "29:1I9Is_Sx0OIy2rQ7Xz1lcaPKlO9eqmBRTBuW6XzXFKLOdvM3eomUkokRsLbCv"
NB_TENANT = uuid.UUID("aaaaaaaa-1111-4111-8111-111111111111")
OTHER_NB_TENANT = uuid.UUID("bbbbbbbb-2222-4222-8222-222222222222")


def bot_added_to_team(**from_):
    """conversationUpdate as Microsoft documents it for a bot added to a team."""
    return Activity().deserialize(
        {
            "membersAdded": [{"id": BOT}],
            "type": "conversationUpdate",
            "timestamp": "2026-10-28T10:02:00.000Z",
            "id": "f:5f85c2ad",
            "channelId": "msteams",
            "serviceUrl": SERVICE_URL,
            "from": {"id": INSTALLER, "aadObjectId": "4c1a8e0b-1b64-4a67-b8e2-6c2d3a0a8d35", **from_},
            "conversation": {"isGroup": True, "conversationType": "channel", "id": TEAM_THREAD},
            "recipient": {"id": BOT, "name": "Nubi"},
            "channelData": {
                "team": {"aadGroupId": TEAM_GROUP, "name": "Platform", "id": TEAM_THREAD},
                "eventType": "teamMemberAdded",
                "tenant": {"id": AAD_TENANT},
            },
        }
    )


def channel_message(conversation_type="channel"):
    """A channel @mention, shaped like Microsoft's documented message sample."""
    channel_data = {"tenant": {"id": AAD_TENANT}}
    if conversation_type == "channel":
        channel_data.update(
            teamsChannelId=TEAM_THREAD,
            teamsTeamId=TEAM_THREAD,
            channel={"id": TEAM_THREAD},
            team={"id": TEAM_THREAD, "aadGroupId": TEAM_GROUP},
        )
    return Activity().deserialize(
        {
            "type": "message",
            "id": "1481567603816",
            "channelId": "msteams",
            "serviceUrl": SERVICE_URL,
            "from": {"id": INSTALLER, "name": "Sam Patel", "aadObjectId": "7faf80cb-0000-4000-8000-000000000001"},
            "conversation": {
                "isGroup": conversation_type != "personal",
                "conversationType": conversation_type,
                "id": f"{TEAM_THREAD};messageid=1481567603816" if conversation_type == "channel" else "a:1abc",
            },
            "recipient": {"id": BOT, "name": "Nubi"},
            "text": "<at>Nubi</at> how is the infra?",
            "textFormat": "plain",
            "entities": [{"type": "mention", "mentioned": {"id": BOT, "name": "Nubi"}, "text": "<at>Nubi</at>"}],
            "channelData": channel_data,
        }
    )


def member(upn="dana@contoso.com", tenant=AAD_TENANT, role="user", email="Dana.Lee@contoso.com"):
    """The Bot Connector's get-member response, as Microsoft documents it."""
    payload = {
        "id": INSTALLER,
        "objectId": "9d3e08f9-a7ae-43aa-a4d3-de3f319a8a9c",
        "name": "Dana Lee",
        "givenName": "Dana",
        "surname": "Lee",
        "email": email,
        "userPrincipalName": upn,
        "tenantId": tenant,
        "userRole": role,
    }
    return ChannelAccount.deserialize({k: v for k, v in payload.items() if v is not None})


def _http_error(status):
    error = HttpOperationError.__new__(HttpOperationError)
    error.response = SimpleNamespace(status_code=status, headers={})
    return error


def _sender(found=None, error=None, team_name="", team_error=None):
    sender = bs.TeamsBotSender.__new__(bs.TeamsBotSender)
    sender.app_id = "app-1"
    sender._credentials = object()
    sender._channel_cache = {}
    connector, teams_connector = mock.Mock(), mock.Mock()
    if error is not None:
        connector.conversations.get_conversation_member.side_effect = error
    else:
        connector.conversations.get_conversation_member.return_value = found if found is not None else member()
    teams_connector.teams.get_team_details.return_value = SimpleNamespace(name=team_name)
    teams_connector.teams.get_team_details.side_effect = team_error
    sender._connector = lambda service_url: connector
    sender._teams_connector = lambda service_url: teams_connector
    return sender


@pytest.fixture(autouse=True)
def signing_key(monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "test-signing-key")
    monkeypatch.setattr(settings.ms_teams, "client_id", "app-1")
    cl._identities.clear()
    yield
    cl._identities.clear()


def _params(token):
    payload = json.loads(base64.urlsafe_b64decode(token + "=" * (-len(token) % 4)))
    return payload["body"]["action_params"], payload


def _token_of(url):
    return url.split("#t=", 1)[1]


def _link(activity, sender=None):
    with mock.patch.object(cl, "get_sender", return_value=sender or _sender()):
        return asyncio.run(cl.link_url(activity))


def _expected_installer(upn):
    message = ("ms_teams_connect_installer:" + upn).encode()
    return hmac.new(b"test-signing-key", message, hashlib.sha256).hexdigest()


def _no_link_logs(caplog):
    return [r.getMessage() for r in caplog.records if r.name == cl.LOG.name and "No connect link" in r.getMessage()]


# ------------------------------- the link -------------------------------


def test_the_link_carries_the_team_and_only_a_hash_of_whoever_added_the_bot():
    url = _link(bot_added_to_team())
    assert url.startswith(f"{settings.base_url}/integrations/ms-teams/connect#t=")
    params, payload = _params(_token_of(url))
    assert params == {
        "aad_tenant_id": AAD_TENANT,
        "team_aad_group_id": TEAM_GROUP,
        "team_thread_id": TEAM_THREAD,
        "team_name": "Platform",
        "service_url": SERVICE_URL,
        "app_id": "11111111-2222-4333-8444-555555555555",
        "installer": _expected_installer("dana@contoso.com"),
    }
    text = json.dumps(payload).lower()
    assert "dana" not in text and "contoso" not in text, "no sign-in name, email or domain in a channel-visible link"
    assert payload["kid"] == card_actions.KEY_ID
    assert payload["body"]["action_name"] == "ms_teams_connect" and payload["body"]["origin"] == "ms_teams"


def test_the_installer_hash_is_keyed_and_ignores_case_and_surrounding_whitespace(monkeypatch):
    expected = _expected_installer("dana@contoso.com")
    assert cl.installer_hash("dana@contoso.com") == expected
    assert cl.installer_hash("  Dana@CONTOSO.com\n") == expected
    assert cl.installer_hash("sam@contoso.com") != expected
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "another-key")
    assert cl.installer_hash("dana@contoso.com") != expected


def test_the_identity_comes_from_the_sign_in_name_not_the_email():
    found = member(upn="dlee@contoso.com", email="Dana.Lee@fabrikam.example")
    params, _ = _params(_token_of(_link(bot_added_to_team(), _sender(found))))
    assert params["installer"] == _expected_installer("dlee@contoso.com")
    cl._identities.clear()
    no_upn = member(upn=None, email="dana@contoso.com")
    assert _link(bot_added_to_team(), _sender(no_upn)) is None, "the email never stands in for a missing UPN"


def test_a_mixed_case_sign_in_name_is_hashed_lowercased():
    params, _ = _params(_token_of(_link(bot_added_to_team(), _sender(member(upn="Dana@CONTOSO.com")))))
    assert params["installer"] == _expected_installer("dana@contoso.com")


def test_get_member_identity_reads_only_additional_properties():
    sender = _sender(member(upn="Dana@Contoso.com"))
    assert sender.get_member_identity(SERVICE_URL, TEAM_THREAD, INSTALLER) == {
        "upn": "Dana@Contoso.com",
        "tenant_id": AAD_TENANT,
        "user_role": "user",
    }


def test_the_team_name_is_read_from_microsoft_when_the_event_has_none_and_never_the_id():
    def nameless():
        activity = bot_added_to_team()
        activity.channel_data["team"].pop("name")
        return activity

    params, _ = _params(_token_of(_link(nameless(), _sender(team_name="Platform"))))
    assert params["team_name"] == "Platform"
    params, _ = _params(_token_of(_link(nameless(), _sender(team_name=""))))
    assert params["team_name"] == ""
    params, _ = _params(_token_of(_link(nameless(), _sender(team_error=_http_error(403)))))
    assert params["team_name"] == ""


@pytest.mark.parametrize(
    "found, reason",
    [
        (member(upn=""), "no_upn"),
        (member(upn="sam_fabrikam.example#EXT#@contoso.onmicrosoft.com"), "guest"),
        (member(role="guest"), "role"),
        (member(role=None), "role"),
        (member(tenant=OTHER_AAD_TENANT), "cross_tenant"),
        (member(upn="dana@outlook.com"), "public_domain"),
        (member(upn="dana@gmail.com"), "public_domain"),
        (member(upn="admin@contoso.onmicrosoft.com"), "onmicrosoft"),
        (member(upn="dana@contoso.com@evil.example"), "malformed_upn"),
        (member(upn="dana@contoso.com."), "malformed_upn"),
        (member(upn="dåna@contoso.com"), "malformed_upn"),
    ],
    ids=[
        "empty",
        "guest-ext",
        "guest-role",
        "no-role",
        "cross-tenant",
        "outlook",
        "gmail",
        "admin-only",
        "two-at",
        "dot",
        "non-ascii",
    ],
)
def test_no_link_for_someone_who_cant_have_one_and_only_the_reason_is_logged(caplog, found, reason):
    caplog.set_level("INFO", logger=cl.LOG.name)
    assert _link(bot_added_to_team(), _sender(found)) is None
    [message] = _no_link_logs(caplog)
    assert message.endswith(f": {reason}")
    logged = " ".join(r.getMessage() for r in caplog.records).lower()
    upn = found.additional_properties.get("userPrincipalName")
    assert not upn or upn.lower() not in logged, "never the sign-in name"
    assert "dana.lee@contoso.com" not in logged and "dana" not in logged, "never the email or the name"


def test_no_link_for_a_personal_microsoft_account(caplog):
    caplog.set_level("INFO", logger=cl.LOG.name)
    consumer = member(upn="dana@contoso.com", tenant=cl.CONSUMER_TENANT_ID)
    activity = bot_added_to_team()
    activity.channel_data["tenant"]["id"] = cl.CONSUMER_TENANT_ID
    assert _link(activity, _sender(consumer)) is None
    assert _no_link_logs(caplog)[0].endswith(": consumer_tenant")


@pytest.mark.parametrize("status", [404, 403, 500])
def test_no_link_when_the_lookup_fails(caplog, status):
    caplog.set_level("INFO", logger=cl.LOG.name)
    assert _link(bot_added_to_team(), _sender(error=_http_error(status))) is None
    assert _no_link_logs(caplog)[0].endswith(": lookup_failed")


def test_no_link_without_the_signing_key_or_outside_a_team(caplog, monkeypatch):
    caplog.set_level("INFO", logger=cl.LOG.name)
    personal = bot_added_to_team()
    personal.conversation.conversation_type = "personal"
    personal.channel_data.pop("team")
    assert _link(personal) is None
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "")
    assert _link(bot_added_to_team()) is None
    assert [m.rsplit(": ", 1)[1] for m in _no_link_logs(caplog)] == ["not_channel", "key_unset"]


def test_lookups_are_cached_per_member_apart_from_the_email_cache():
    sender = _sender()
    emails_before = dict(identity._emails)
    _link(bot_added_to_team(), sender)
    _link(bot_added_to_team(), sender)
    assert sender._connector(SERVICE_URL).conversations.get_conversation_member.call_count == 1
    assert (AAD_TENANT, INSTALLER) in cl._identities
    assert identity._emails == emails_before


def test_a_failed_lookup_is_not_cached():
    _link(bot_added_to_team(), _sender(error=_http_error(404)))
    assert cl._identities == {}


def test_the_lookup_cache_drops_expired_entries_on_write():
    now = cl.time.monotonic()
    for i in range(5):
        cl._identities[(AAD_TENANT, f"29:old-{i}")] = ({"upn": f"old{i}@contoso.com"}, now - 1)
    cl._identities[(AAD_TENANT, "29:live")] = ({"upn": "live@contoso.com"}, now + 60)
    cl._remember((AAD_TENANT, "29:new"), {"upn": "new@contoso.com"})
    assert list(cl._identities) == [(AAD_TENANT, "29:live"), (AAD_TENANT, "29:new")]


def test_the_lookup_cache_is_capped_dropping_the_oldest():
    assert cl.IDENTITY_CACHE_SIZE == 1000
    for i in range(cl.IDENTITY_CACHE_SIZE + 5):
        cl._remember((AAD_TENANT, f"29:m-{i}"), {"upn": f"m{i}@contoso.com"})
    assert len(cl._identities) == cl.IDENTITY_CACHE_SIZE
    assert (AAD_TENANT, "29:m-4") not in cl._identities and (AAD_TENANT, "29:m-5") in cl._identities
    assert (AAD_TENANT, f"29:m-{cl.IDENTITY_CACHE_SIZE + 4}") in cl._identities


# ------------------------------- comparator -------------------------------


@pytest.mark.parametrize(
    "value, domain",
    [
        ("dana@contoso.com", "contoso.com"),
        ("Dana@Contoso.COM", "contoso.com"),
        ("dana@eu.contoso.com", "eu.contoso.com"),
        ("dana@contoso.com@x.example", None),
        ("dana.contoso.com", None),
        ("dana@contoso.com.", None),
        (" dana@contoso.com", None),
        ("dana@contoso.com\n", None),
        ("dana@cöntoso.com", None),
        ("dana@", None),
        ("", None),
        (None, None),
    ],
)
def test_email_domain(value, domain):
    assert cl.email_domain(value) == domain


# ------------------------------- verifying -------------------------------


def _mint(**overrides):
    params = {
        "aad_tenant_id": AAD_TENANT,
        "team_aad_group_id": TEAM_GROUP,
        "team_thread_id": TEAM_THREAD,
        "team_name": "Platform",
        "service_url": SERVICE_URL,
        "app_id": "app-1",
        "installer": cl.installer_hash("dana@contoso.com"),
        **overrides,
    }
    return cl.mint(params)


def _encode(payload):
    return base64.urlsafe_b64encode(json.dumps(payload).encode()).decode().rstrip("=")


def _minted_at(seconds_ago):
    now = card_actions.time.time()
    with mock.patch.object(card_actions.time, "time", return_value=now - seconds_ago):
        return _mint()


def test_a_fresh_link_verifies():
    params, refusal = cl.verify(_mint())
    assert refusal is None and params["installer"] == _expected_installer("dana@contoso.com")


def test_a_link_lasts_seven_days():
    assert cl.verify(_minted_at(card_actions.MAX_AGE_SECONDS - 60))[1] is None
    assert cl.verify(_minted_at(card_actions.MAX_AGE_SECONDS + 60)) == (None, cl.EXPIRED)
    assert cl.verify(_minted_at(-200))[1] is None, "a few minutes of clock skew is allowed"
    assert cl.verify(_minted_at(-400)) == (None, cl.INVALID)


def test_a_tampered_link_is_invalid():
    _, payload = _params(_mint())
    payload["body"]["action_params"]["installer"] = cl.installer_hash("sam@contoso.com")
    assert cl.verify(_encode(payload)) == (None, cl.INVALID)


def test_another_actions_data_or_key_or_origin_is_invalid():
    params = _params(_mint())[0]
    assert cl.verify(_encode(json.loads(card_actions.sign("suppress", params)))) == (None, cl.INVALID)
    _, payload = _params(_mint())
    payload["kid"] = "2"
    assert cl.verify(_encode(payload)) == (None, cl.INVALID)
    body = ActionRequestBody(timestamp=int(card_actions.time.time()), action_name=cl.ACTION_NAME, action_params=params)
    for origin in (None, "slack"):
        body.origin = origin
        signed = {
            "kid": card_actions.KEY_ID,
            "body": body.model_dump(exclude_none=True),
            "signature": sign_action_request(body, "test-signing-key"),
        }
        assert cl.verify(_encode(signed)) == (None, cl.INVALID), origin


@pytest.mark.parametrize("token", ["", "not base64!", _encode({"kid": "1"}), None, 7])
def test_garbage_is_invalid(token):
    assert cl.verify(token) == (None, cl.INVALID)


def test_a_link_signed_with_another_key_is_invalid(monkeypatch):
    token = _mint()
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "rotated-key")
    assert cl.verify(token) == (None, cl.INVALID)
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "")
    assert cl.verify(token) == (None, cl.INVALID)


# ------------------------------- redeeming -------------------------------


def _session_with(*rows):
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session

    from notifications_server.models.models import (
        Base,
        ChannelAccountMapping,
        Integration,
        IntegrationConfigValue,
        MessagingPlatform,
    )

    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(
        engine,
        tables=[
            Integration.__table__,
            IntegrationConfigValue.__table__,
            MessagingPlatform.__table__,
            ChannelAccountMapping.__table__,
        ],
    )
    session = Session(engine)
    session.add_all(rows)
    session.commit()
    return session


def _connection(name, tenant_id, status="enabled", type_="ms_teams"):
    from notifications_server.models.models import Integration

    return Integration(
        id=uuid.uuid4(), tenant_id=tenant_id, type=type_, source="user", name=name, status=status, labels={}
    )


def _rows(session, type_):
    from notifications_server.models.models import Integration, IntegrationConfigValue

    out = []
    for integ in session.query(Integration).filter(Integration.type == type_).all():
        rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
        out.append((integ, mi._decrypt_config(rows)))
    return out


@pytest.fixture
def audit(monkeypatch):
    audit = mock.Mock()
    monkeypatch.setattr(tb, "create_audit_request", audit)
    monkeypatch.setattr(tb, "get_user_by_email_address", lambda email: None)
    monkeypatch.setattr(tb, "Cache", mock.Mock())
    monkeypatch.setattr(tb, "_lock", lambda session, workspaces: None)
    monkeypatch.setattr(settings.ms_teams, "client_id", "app-1")
    return audit


def test_whoever_added_the_bot_binds_the_team_to_their_workspace(audit):
    session = _session_with()
    with mock.patch.object(session, "close") as close:
        assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == tb.CONNECTED
    close.assert_not_called()
    [(connection, cfg)] = _rows(session, "ms_teams")
    assert (connection.name, str(connection.tenant_id), connection.status) == (AAD_TENANT, str(NB_TENANT), "enabled")
    assert cfg == {"app_id": "app-1", "installed_by": "dana@contoso.com"}, "installed_by is who confirmed"
    [(team, team_cfg)] = _rows(session, "ms_teams_team")
    assert (team.name, str(team.tenant_id), team.status) == (TEAM_GROUP, str(NB_TENANT), "enabled")
    assert (team_cfg["team_name"], team_cfg["aad_tenant_id"]) == ("Platform", AAD_TENANT)
    assert tb.BOUND_AT in team_cfg
    assert audit.call_args.kwargs["event_attr"] == {
        "status": "success",
        "team": TEAM_GROUP,
        "confirmed_by": "dana@contoso.com",
    }


@pytest.mark.parametrize("email", ["Dana@Contoso.com", "  dana@contoso.com ", "DANA@CONTOSO.COM\n"])
def test_the_installers_login_matches_whatever_its_case_or_surrounding_whitespace(audit, email):
    assert cl.redeem(_session_with(), _mint(), NB_TENANT, email) == tb.CONNECTED


def test_someone_else_on_the_same_domain_is_refused(audit):
    session = _session_with()
    assert cl.redeem(session, _mint(), NB_TENANT, "sam@contoso.com") == cl.WRONG_USER
    assert _rows(session, "ms_teams") == []
    audit.assert_not_called()


@pytest.mark.parametrize(
    "email",
    ["", "dana", "dana@contoso.com@x.example", "dåna@contoso.com", "dana@fabrikam.example", None],
)
def test_a_malformed_or_other_login_is_refused_as_the_wrong_user(audit, email):
    session = _session_with()
    assert cl.redeem(session, _mint(), NB_TENANT, email) == cl.WRONG_USER
    assert _rows(session, "ms_teams") == []


def test_redeeming_compares_the_hashes_in_constant_time(audit):
    with mock.patch.object(cl.hmac, "compare_digest", wraps=cl.hmac.compare_digest) as compare:
        assert cl.redeem(_session_with(), _mint(), NB_TENANT, "sam@contoso.com") == cl.WRONG_USER
    # The link's own signature is checked the same way first.
    assert compare.call_args == mock.call(
        _expected_installer("sam@contoso.com").encode(), _expected_installer("dana@contoso.com").encode()
    )


def test_the_domain_refusals_are_gone():
    assert (cl.INVALID, cl.EXPIRED, cl.WRONG_USER) == ("invalid", "expired", "wrong_user")
    for removed in ("PUBLIC_DOMAIN", "DOMAIN_MISMATCH"):
        assert not hasattr(cl, removed)


@pytest.mark.parametrize("missing", ["installer", "aad_tenant_id", "team_aad_group_id"])
def test_a_link_without_its_tenant_team_or_installer_is_invalid(audit, missing):
    assert cl.redeem(_session_with(), _mint(**{missing: ""}), NB_TENANT, "dana@contoso.com") == cl.INVALID


def test_a_link_bound_to_a_domain_rather_than_a_person_is_invalid(audit):
    token = cl.mint(
        {
            "aad_tenant_id": AAD_TENANT,
            "team_aad_group_id": TEAM_GROUP,
            "team_thread_id": TEAM_THREAD,
            "team_name": "Platform",
            "service_url": SERVICE_URL,
            "app_id": "app-1",
            "upn_domain": "contoso.com",
        }
    )
    session = _session_with()
    assert cl.redeem(session, token, NB_TENANT, "dana@contoso.com") == cl.INVALID
    assert _rows(session, "ms_teams") == []


def test_expired_and_invalid_links_connect_nothing(audit):
    session = _session_with()
    expired = _minted_at(card_actions.MAX_AGE_SECONDS + 60)
    assert cl.redeem(session, expired, NB_TENANT, "dana@contoso.com") == cl.EXPIRED
    assert cl.redeem(session, "garbage", NB_TENANT, "dana@contoso.com") == cl.INVALID
    assert _rows(session, "ms_teams") == []


def test_a_team_bound_to_another_workspace_is_refused(audit):
    session = _session_with(_connection(TEAM_GROUP, OTHER_NB_TENANT, type_="ms_teams_team"))
    assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == tb.TEAM_LINKED_ELSEWHERE
    assert [str(t.tenant_id) for t, _ in _rows(session, "ms_teams_team")] == [str(OTHER_NB_TENANT)]
    assert _rows(session, "ms_teams") == []


def test_another_workspace_on_the_same_microsoft_tenant_doesnt_block_a_bind(audit):
    session = _session_with(_connection(AAD_TENANT, OTHER_NB_TENANT))
    assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == tb.CONNECTED
    assert sorted(str(c.tenant_id) for c, _ in _rows(session, "ms_teams")) == sorted(
        [str(NB_TENANT), str(OTHER_NB_TENANT)]
    )


def test_a_link_minted_before_the_team_was_released_is_expired(audit):
    from notifications_server.models.models import IntegrationConfigValue

    released = _connection(TEAM_GROUP, NB_TENANT, status="released", type_="ms_teams_team")
    later = (datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(minutes=1)).replace(tzinfo=None)
    stamp = IntegrationConfigValue(
        id=uuid.uuid4(), integration_id=released.id, name=tb.RELEASED_AT, value=later.isoformat(), is_encrypted=False
    )
    session = _session_with(released, stamp)
    assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == cl.EXPIRED
    assert [t.status for t, _ in _rows(session, "ms_teams_team")] == ["released"]

    stamp.value = (later - datetime.timedelta(minutes=10)).isoformat()
    session.merge(stamp)
    session.commit()
    assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == tb.CONNECTED


def test_a_team_already_bound_to_this_workspace_says_so(audit):
    session = _session_with(_connection(TEAM_GROUP, NB_TENANT, type_="ms_teams_team"))
    assert cl.redeem(session, _mint(), NB_TENANT, "dana@contoso.com") == tb.ALREADY_CONNECTED


# ------------------------------- the endpoint -------------------------------


def _post(code, tenant=str(NB_TENANT)):
    from notifications_server.routers import tools

    request = SimpleNamespace(headers={"tenant-id": tenant, "x-user-email": "dana@contoso.com"})
    with (
        mock.patch.object(tools, "Session"),
        mock.patch.object(tools.connect_link, "redeem", return_value=code) as redeem,
    ):
        response = asyncio.run(tools.ms_teams_link(request, tools.MsTeamsLinkRequest(token="the-token")))
    return response, redeem


def test_the_endpoint_answers_connected():
    response, redeem = _post(tb.CONNECTED)
    assert response == {"status": "connected"}
    assert redeem.call_args.args[1:] == ("the-token", NB_TENANT, "dana@contoso.com")


@pytest.mark.parametrize(
    "code",
    [
        cl.WRONG_USER,
        cl.EXPIRED,
        cl.INVALID,
        tb.TEAM_LINKED_ELSEWHERE,
        tb.OTHER_MICROSOFT_TENANT,
        tb.ALREADY_CONNECTED,
    ],
)
def test_the_endpoint_answers_a_refusal_with_its_code_only(code):
    response, _ = _post(code)
    assert response.status_code == 400
    assert json.loads(response.body) == {"code": code}


@pytest.mark.parametrize("tenant", ["", "not-a-uuid"])
def test_the_endpoint_needs_the_tenant(tenant):
    from fastapi import HTTPException

    with pytest.raises(HTTPException) as exc:
        _post(tb.CONNECTED, tenant=tenant)
    assert exc.value.status_code == 403


def test_the_endpoint_checks_the_action_token_like_every_rpc_endpoint():
    from notifications_server.routers import tools
    from notifications_server.routers.common import verify_action_token

    route = next(r for r in tools.router.routes if r.path == "/api/integrations/link/ms-teams")
    assert route.methods == {"POST"}
    assert [d.dependency for d in route.dependencies] == [verify_action_token]


def test_a_missing_token_is_redeemed_as_invalid():
    from notifications_server.routers import tools

    assert tools.MsTeamsLinkRequest().token == ""
    assert cl.verify(tools.MsTeamsLinkRequest().token) == (None, cl.INVALID)


# ------------------------------- welcome card -------------------------------


def _welcome(activity, sender=None, owner=None):
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    service.teams_reply_with_card = mock.AsyncMock(return_value=True)
    with (
        mock.patch.object(common_module, "workspaces_for", return_value={owner} if owner else set()),
        mock.patch.object(cl, "get_sender", return_value=sender or _sender()),
    ):
        asyncio.run(service.teams_send_welcome_message(activity))
    return service.teams_reply_with_card.call_args.args[1]


def _texts(card):
    return [b.get("text", "") for b in card["body"]]


def _buttons(card):
    actions = list(card.get("actions") or [])
    for block in card["body"]:
        actions += block.get("actions") or []
    return actions


ASK_AN_ADMIN = "Ask a Acme admin to @mention Nubi here to connect this team."


def test_the_welcome_carries_a_link_for_the_person_who_added_the_bot(monkeypatch):
    monkeypatch.setattr(settings.urls, "branding_name", "Acme")
    monkeypatch.setattr(settings.urls, "assistant_name", "Nubi")
    card = _welcome(bot_added_to_team())
    [button] = _buttons(card)
    assert button["title"] == "Connect Microsoft Teams" and "#t=" in button["url"]
    assert _params(_token_of(button["url"]))[0]["installer"] == _expected_installer("dana@contoso.com")
    texts = _texts(card)
    assert "Connect Microsoft Teams in Acme to get alerts and ask questions here." in texts
    assert texts[-1] == (
        "This button works for 7 days and only for the person who added Nubi. "
        "After that, @mention Nubi here for a new one."
    )
    assert [b["type"] for b in card["body"]][-2:] == ["ActionSet", "TextBlock"], "the line follows the button"
    assert "Sign up" in json.dumps(card)


@pytest.mark.parametrize(
    "sender",
    [
        _sender(member(upn="admin@contoso.onmicrosoft.com")),
        _sender(member(role="guest")),
        _sender(member(upn="dana@gmail.com")),
        _sender(error=_http_error(404)),
        _sender(error=bs.TeamsBotError("Microsoft Teams request failed: ConnectionError")),
    ],
    ids=["admin-only", "guest", "public", "404", "bot-error"],
)
def test_the_welcome_says_to_ask_an_admin_when_the_installer_cant_have_a_link(monkeypatch, sender):
    monkeypatch.setattr(settings.urls, "branding_name", "Acme")
    monkeypatch.setattr(settings.urls, "assistant_name", "Nubi")
    card = _welcome(bot_added_to_team(), sender)
    assert _buttons(card) == []
    assert _texts(card)[-1] == ASK_AN_ADMIN
    assert "#t=" not in json.dumps(card)
    assert "Welcome to Nubi!" in json.dumps(card), "the welcome is still sent"


def test_the_welcome_logs_why_there_is_no_link_and_never_who(caplog):
    caplog.set_level("INFO", logger=cl.LOG.name)
    _welcome(bot_added_to_team(), _sender(member(upn="dana@gmail.com", email="dana@gmail.com")))
    [message] = _no_link_logs(caplog)
    assert message == f"No connect link on the Teams welcome card for Microsoft tenant {AAD_TENANT}: public_domain"
    assert "dana" not in " ".join(r.getMessage() for r in caplog.records).lower()


def test_the_welcome_without_an_installer_has_no_button(caplog, monkeypatch):
    monkeypatch.setattr(settings.urls, "branding_name", "Acme")
    monkeypatch.setattr(settings.urls, "assistant_name", "Nubi")
    caplog.set_level("INFO", logger=cl.LOG.name)
    activity = bot_added_to_team()
    activity.from_property = None
    card = _welcome(activity)
    assert _buttons(card) == [] and _texts(card)[-1] == ASK_AN_ADMIN
    assert _no_link_logs(caplog)[0].endswith(": no_installer")


def test_a_connected_team_gets_todays_welcome():
    sender = _sender()
    assert _welcome(bot_added_to_team(), sender, owner=str(NB_TENANT)) == CommonService.create_teams_welcome_card(True)
    sender._connector(SERVICE_URL).conversations.get_conversation_member.assert_not_called()


def test_no_link_on_the_welcome_without_the_signing_key(caplog, monkeypatch):
    caplog.set_level("INFO", logger=cl.LOG.name)
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "")
    sender = _sender()
    card = _welcome(bot_added_to_team(), sender)
    assert card == CommonService.create_teams_welcome_card(False), "today's card"
    sender._connector(SERVICE_URL).conversations.get_conversation_member.assert_not_called()
    assert _no_link_logs(caplog)[0].endswith(": key_unset")


@pytest.mark.parametrize("conversation_type", ["personal", "groupChat"])
def test_no_link_on_the_welcome_outside_a_team(conversation_type):
    activity = bot_added_to_team()
    activity.conversation.conversation_type = conversation_type
    activity.channel_data.pop("team")
    assert _welcome(activity) == CommonService.create_teams_welcome_card(False)


# ------------------------------- replies to messages -------------------------------


def _message(activity, workspaces=(), user_email=None, claimed=True, sender=None):
    events = Events.__new__(Events)
    events.session = mock.Mock()
    events.common_service = SimpleNamespace(
        get_teams_user_info=mock.AsyncMock(return_value=user_email),
        teams_reply=mock.AsyncMock(),
        teams_reply_with_card=mock.AsyncMock(return_value=True),
        create_teams_not_connected_card=CommonService.create_teams_not_connected_card,
        create_teams_connect_card=CommonService.create_teams_connect_card,
    )
    with (
        mock.patch.object(events_module, "Session"),
        mock.patch.object(events_module, "workspaces_for", return_value=set(workspaces)),
        mock.patch.object(cl, "get_sender", return_value=sender or _sender()) as get_sender,
        mock.patch.object(cl.Cache, "claim_teams_connect_link", return_value=claimed) as claim,
    ):
        asyncio.run(events.handle_message_event(activity))
    return SimpleNamespace(common=events.common_service, get_sender=get_sender, claim=claim)


@pytest.fixture
def acme(monkeypatch):
    monkeypatch.setattr(settings.urls, "branding_name", "Acme")
    monkeypatch.setattr(settings.urls, "assistant_name", "Nubi")


def test_a_mention_in_a_team_not_connected_gets_a_link_for_whoever_mentioned_the_bot(acme):
    cl._identities.clear()
    result = _message(channel_message())
    card = result.common.teams_reply_with_card.call_args.args[1]
    [button] = _buttons(card)
    assert button["title"] == "Connect this team" and "#t=" in button["url"]
    params = _params(_token_of(button["url"]))[0]
    assert params["installer"] == _expected_installer("dana@contoso.com")
    assert params["team_aad_group_id"] == TEAM_GROUP
    assert result.claim.call_args.args == (TEAM_GROUP, "7faf80cb-0000-4000-8000-000000000001", cl.MENTION_LINK_SECONDS)
    result.common.get_teams_user_info.assert_not_awaited()


def test_a_message_that_doesnt_mention_the_bot_gets_no_link_and_keeps_the_slot(acme):
    activity = channel_message()
    activity.entities = [Entity().deserialize({"type": "mention", "mentioned": {"id": "29:someone-else"}})]
    result = _message(activity)
    card = result.common.teams_reply_with_card.call_args.args[1]
    assert _texts(card) == [ASK_AN_ADMIN] and _buttons(card) == []
    result.claim.assert_not_called()
    result.get_sender.assert_not_called()


def test_a_message_without_a_conversation_type_but_with_team_data_is_a_team_message(acme):
    activity = channel_message()
    activity.conversation.conversation_type = None
    result = _message(activity, claimed=False)
    assert result.common.teams_reply.call_args.args[1] == cl.LINK_ALREADY_SENT
    result.common.get_teams_user_info.assert_not_awaited()


def test_a_second_mention_within_the_window_points_to_the_first_link(acme):
    cl._identities.clear()
    result = _message(channel_message(), claimed=False)
    result.common.teams_reply_with_card.assert_not_awaited()
    assert result.common.teams_reply.call_args.args[1] == cl.LINK_ALREADY_SENT


def test_a_mention_from_someone_who_cant_have_a_link_says_to_ask_an_admin(acme):
    cl._identities.clear()
    result = _message(channel_message(), sender=_sender(member(role="guest")))
    card = result.common.teams_reply_with_card.call_args.args[1]
    assert _texts(card) == [ASK_AN_ADMIN] and _buttons(card) == []
    assert "#t=" not in json.dumps(card)
    result.claim.assert_not_called()


def test_without_the_signing_key_a_team_not_connected_says_to_ask_an_admin(acme, monkeypatch):
    monkeypatch.setattr(settings.ms_teams, "action_signing_key", "")
    result = _message(channel_message())
    assert _texts(result.common.teams_reply_with_card.call_args.args[1]) == [ASK_AN_ADMIN]
    result.get_sender.assert_not_called()
    result.common.get_teams_user_info.assert_not_awaited()


def test_a_connected_teams_no_account_reply_is_unchanged():
    with mock.patch.object(events_module, "get_user_not_found_message", return_value="no account"):
        result = _message(channel_message(), workspaces=[str(NB_TENANT)])
    result.common.get_teams_user_info.assert_awaited_once()
    result.common.teams_reply_with_card.assert_not_awaited()
    assert result.common.teams_reply.call_args.args[1] == "no account"


def test_no_connect_card_in_a_personal_chat():
    result = _message(channel_message("personal"))
    result.common.get_teams_user_info.assert_awaited_once()
    result.common.teams_reply_with_card.assert_not_awaited()


# ------------------------------- get_teams_user_info -------------------------------


def _user_info(workspaces, email):
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    service.session.query.return_value.filter.return_value.first.return_value = None
    with (
        mock.patch.object(common_module, "eligible", return_value=workspaces),
        mock.patch.object(common_module, "sender_email", mock.AsyncMock(return_value=email)),
    ):
        return asyncio.run(service.get_teams_user_info("aad-user", AAD_TENANT, channel_message()))


def test_get_teams_user_info_keeps_its_contract():
    assert _user_info(set(), "dana@contoso.com") is None, "no team connected in the Microsoft tenant: None"
    assert _user_info({str(NB_TENANT)}, None) is None
    assert _user_info({str(NB_TENANT)}, "dana@contoso.com") == "dana@contoso.com"
