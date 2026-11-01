"""Teams bot installs are recorded as ms_teams_team integrations, and the
Microsoft tenant id names the ms_teams integration."""

import asyncio
from types import SimpleNamespace
from unittest import mock

from botbuilder.schema import Activity

from notifications_server.services.ms_teams import bot_installations as bi
from notifications_server.services.ms_teams.ms_teams import aad_tenant_id_of

SERVICE_URL = "https://smba.example.com/teams/"
NB_TENANT = "aaaaaaaa-1111-4111-8111-111111111111"


def _activity(activity_type, conversation_type="channel", action=None, event_type=None, **extra):
    channel_data = {"tenant": {"id": "tenant-a"}}
    if conversation_type == "channel":
        channel_data["team"] = {"id": "19:general@thread.tacv2", "name": "Platform", "aadGroupId": "group-1"}
    if event_type:
        channel_data["eventType"] = event_type
    payload = {
        "type": activity_type,
        "serviceUrl": SERVICE_URL,
        "from": {"id": "29:user", "name": "Dana", "aadObjectId": "user-1"},
        "recipient": {"id": "28:app-1", "name": "Bot"},
        "conversation": {"id": "19:general@thread.tacv2", "conversationType": conversation_type},
        "channelData": channel_data,
    }
    if action:
        payload["action"] = action
    payload.update(extra)
    return Activity().deserialize(payload)


def _actions(activity):
    return [c["action"] for c in bi.team_changes(activity)]


def test_team_install_carries_the_team_fields():
    assert bi.team_changes(_activity("installationUpdate", action="add")) == [
        {
            "action": "upsert",
            "from_message": False,
            "team_aad_group_id": "group-1",
            "team_thread_id": "19:general@thread.tacv2",
            "team_name": "Platform",
            "service_url": SERVICE_URL,
            "aad_tenant_id": "tenant-a",
            "app_id": "app-1",
        }
    ]


def test_install_actions():
    assert _actions(_activity("installationUpdate", action="add-upgrade")) == ["upsert"]
    assert _actions(_activity("installationUpdate", action="remove")) == ["remove"]
    assert _actions(_activity("installationUpdate", action="remove-upgrade")) == ["remove"]


def test_bot_joining_or_leaving_a_team():
    assert _actions(_activity("conversationUpdate", membersAdded=[{"id": "28:app-1"}])) == ["upsert"]
    assert _actions(_activity("conversationUpdate", membersRemoved=[{"id": "28:app-1"}])) == ["remove"]
    assert _actions(_activity("conversationUpdate", membersAdded=[{"id": "29:someone"}])) == []


def test_team_lifecycle_events():
    for event_type in ("teamRenamed", "teamRestored", "teamUnarchived"):
        assert _actions(_activity("conversationUpdate", event_type=event_type)) == ["upsert"], event_type
    for event_type in ("teamDeleted", "teamHardDeleted", "teamArchived"):
        assert _actions(_activity("conversationUpdate", event_type=event_type)) == ["remove"], event_type
    assert _actions(_activity("conversationUpdate", event_type="channelCreated")) == []


def test_a_message_in_a_team_records_the_team():
    assert _actions(_activity("message", text="hi")) == ["upsert"]


def test_personal_group_and_incomplete_activities_are_not_recorded():
    assert bi.team_changes(_activity("installationUpdate", "personal", action="add")) == []
    assert bi.team_changes(_activity("installationUpdate", "groupChat", action="add")) == []
    no_group = _activity("installationUpdate", action="add")
    no_group.channel_data["team"].pop("aadGroupId")
    assert bi.team_changes(no_group) == []
    no_tenant = _activity("installationUpdate", action="add")
    no_tenant.channel_data.pop("tenant")
    assert bi.team_changes(no_tenant) == []


def _change(action="upsert", **overrides):
    change = bi.team_changes(_activity("installationUpdate", action="add"))[0]
    change.update(action=action, **overrides)
    return change


def _team(session):
    from notifications_server.models.models import Integration, IntegrationConfigValue
    from notifications_server.services import messaging_installations as mi

    integ = session.query(Integration).filter(Integration.type == bi.TEAM_TYPE).one()
    rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
    return integ, mi._decrypt_config(rows)


def test_install_before_connect_records_nothing():
    session = _session_with()
    assert bi.apply_team_change(session, _change()) == bi.NOT_CONNECTED
    from notifications_server.models.models import Integration

    assert session.query(Integration).count() == 0


def test_install_rename_uninstall_and_reinstall():
    session = _session_with(_teams_integration("tenant-a", NB_TENANT))
    assert bi.apply_team_change(session, _change()) == bi.RECORDED
    integ, cfg = _team(session)
    assert (integ.name, str(integ.tenant_id), integ.status) == ("group-1", NB_TENANT, "enabled")
    assert cfg == {
        "team_thread_id": "19:general@thread.tacv2",
        "team_name": "Platform",
        "service_url": SERVICE_URL,
        "aad_tenant_id": "tenant-a",
        "app_id": "app-1",
    }

    assert bi.apply_team_change(session, _change()) == bi.UNCHANGED, "a repeat event writes nothing"

    assert bi.apply_team_change(session, _change(team_name="Platform On-call", team_thread_id="")) == bi.RECORDED
    _, cfg = _team(session)
    assert cfg["team_name"] == "Platform On-call"
    assert cfg["team_thread_id"] == "19:general@thread.tacv2", "an empty field keeps the stored value"

    assert bi.apply_team_change(session, _change("remove")) == bi.REMOVED
    assert _team(session)[0].status == "disabled", "uninstall disables, never deletes"
    assert bi.apply_team_change(session, _change("remove")) == bi.UNCHANGED

    assert bi.apply_team_change(session, _change()) == bi.RECORDED
    assert _team(session)[0].status == "enabled"


def test_a_team_row_moves_with_its_microsoft_tenant():
    other_tenant = "bbbbbbbb-2222-4222-8222-222222222222"
    session = _session_with(_teams_integration("tenant-a", NB_TENANT))
    assert bi.apply_team_change(session, _change()) == bi.RECORDED
    before = _team(session)[0]
    row_id = before.id

    from notifications_server.models.models import Integration

    connection = session.query(Integration).filter(Integration.type == "ms_teams").one()
    session.delete(connection)
    session.add(_teams_integration("tenant-a", other_tenant))
    session.commit()

    assert bi.apply_team_change(session, _change()) == bi.RECORDED
    after = _team(session)[0]
    assert (after.id, after.name) == (row_id, "group-1"), "one row per team: the id is not stable across workspaces"
    assert str(after.tenant_id) == other_tenant


def test_a_concurrent_insert_is_retried_as_an_update():
    from sqlalchemy.exc import IntegrityError

    calls = []

    def flaky(session, change):
        calls.append(change["action"])
        if len(calls) == 1:
            raise IntegrityError("INSERT", {}, Exception("duplicate key"))
        return bi.RECORDED

    session = mock.Mock()
    with mock.patch.object(bi, "_apply_team_change", side_effect=flaky):
        assert bi.apply_team_change(session, _change()) == bi.RECORDED
    session.rollback.assert_called_once()
    assert calls == ["upsert", "upsert"]


def test_messages_from_a_recorded_team_skip_the_database():
    bi._seen_from_messages.clear()
    message = _activity("message", text="hi")
    with (
        mock.patch.object(bi, "apply_team_change", return_value=bi.RECORDED) as apply,
        mock.patch.object(bi, "Session"),
    ):
        asyncio.run(bi.record_team_installations(message, engine=object()))
        asyncio.run(bi.record_team_installations(message, engine=object()))
        assert apply.call_count == 1, "the second message is served from the cache"

        renamed = _activity("message", text="hi")
        renamed.channel_data["team"]["name"] = "Platform On-call"
        asyncio.run(bi.record_team_installations(renamed, engine=object()))
        assert apply.call_count == 2, "a changed team is written"

        asyncio.run(bi.record_team_installations(_activity("installationUpdate", action="remove"), engine=object()))
        asyncio.run(bi.record_team_installations(renamed, engine=object()))
        assert apply.call_count == 4, "install events always write and clear the cache"
    bi._seen_from_messages.clear()


def test_a_team_not_yet_connected_is_not_cached():
    bi._seen_from_messages.clear()
    message = _activity("message", text="hi")
    with (
        mock.patch.object(bi, "apply_team_change", return_value=bi.NOT_CONNECTED) as apply,
        mock.patch.object(bi, "Session"),
    ):
        asyncio.run(bi.record_team_installations(message, engine=object()))
        asyncio.run(bi.record_team_installations(message, engine=object()))
    assert apply.call_count == 2


def test_a_write_the_database_rejects_is_logged_not_raised():
    from sqlalchemy.exc import IntegrityError

    error = IntegrityError("INSERT", {}, Exception("violates foreign key constraint"))
    with (
        mock.patch.object(bi, "apply_team_change", side_effect=error),
        mock.patch.object(bi, "Session"),
        mock.patch.object(bi.LOG, "warning") as warning,
    ):
        asyncio.run(bi.record_team_installations(_activity("installationUpdate", action="add"), engine=object()))
    warning.assert_called_once()


def test_record_team_installations_runs_off_the_loop():
    activity = _activity("installationUpdate", action="add")
    with (
        mock.patch.object(bi, "apply_team_change", return_value=bi.RECORDED) as apply,
        mock.patch.object(bi, "Session"),
    ):
        asyncio.run(bi.record_team_installations(activity, engine=object()))
    assert apply.call_args.args[1]["team_aad_group_id"] == "group-1"


def test_aad_tenant_id_from_account_id():
    assert bi.aad_tenant_id_from_account_id("object-1.tenant-a") == "tenant-a"
    assert bi.aad_tenant_id_from_account_id("tenant-a") == "tenant-a"
    assert bi.aad_tenant_id_from_account_id(None) == ""


def test_connect_names_the_integration_by_tenant_id():
    account = {"home_account_id": "object-1.tenant-from-account"}
    assert aad_tenant_id_of({"id_token_claims": {"tid": "tenant-a"}}, account) == "tenant-a"
    assert aad_tenant_id_of({}, account) == "tenant-from-account"


def _session_with(*rows):
    from sqlalchemy import create_engine
    from sqlalchemy.orm import Session

    from notifications_server.models.models import Base, Integration, IntegrationConfigValue, MessagingPlatform

    engine = create_engine("sqlite:///:memory:")
    Base.metadata.create_all(
        engine, tables=[Integration.__table__, IntegrationConfigValue.__table__, MessagingPlatform.__table__]
    )
    session = Session(engine)
    session.add_all(rows)
    session.commit()
    return session


def _teams_integration(name, tenant_id, status="enabled"):
    import uuid

    from notifications_server.models.models import Integration

    return Integration(
        id=uuid.uuid4(),
        tenant_id=uuid.UUID(tenant_id),
        type="ms_teams",
        source="user",
        name=name,
        status=status,
        labels={},
    )


def test_lookup_by_microsoft_tenant_is_exact():
    from notifications_server.services import messaging_installations as mi

    nb_tenant = "aaaaaaaa-1111-4111-8111-111111111111"
    session = _session_with(
        _teams_integration("tenant-a", nb_tenant),
        _teams_integration("object-9.tenant-ab", "bbbbbbbb-2222-4222-8222-222222222222"),
    )
    found = mi.load_ms_teams_installation_by_aad_tenant(session, "tenant-a")
    assert found is not None and str(found.tenant_id) == nb_tenant
    # The old substring match let "tenant-a" resolve to "object-9.tenant-ab".
    assert str(mi.load_ms_teams_installation_by_aad_tenant(session, "tenant-ab").tenant_id) != nb_tenant
    assert mi.load_ms_teams_installation_by_aad_tenant(session, "nant-a") is None
    assert mi.load_ms_teams_installation_by_aad_tenant(session, "") is None


def test_lookup_accepts_names_saved_before_the_rename():
    import uuid

    from notifications_server.models.models import MessagingPlatform
    from notifications_server.services import messaging_installations as mi

    nb_tenant = "aaaaaaaa-1111-4111-8111-111111111111"
    session = _session_with(_teams_integration("object-1.tenant-a", nb_tenant))
    assert str(mi.load_ms_teams_installation_by_aad_tenant(session, "tenant-a").tenant_id) == nb_tenant

    legacy = _session_with(
        MessagingPlatform(
            id=uuid.uuid4(),
            tenant_id=uuid.UUID(nb_tenant),
            platform="ms_teams",
            team_id="object-1.tenant-b",
            client_id="c",
            token="t",
        )
    )
    found = mi.load_ms_teams_installation_by_aad_tenant(legacy, "tenant-b")
    assert found is not None and found._origin == mi.ORIGIN_LEGACY


def test_tenant_owner_counts_disabled_integrations():
    from notifications_server.services import messaging_installations as mi

    nb_tenant = "aaaaaaaa-1111-4111-8111-111111111111"
    session = _session_with(_teams_integration("tenant-a", nb_tenant, status="disabled"))
    assert mi.load_ms_teams_installation_by_aad_tenant(session, "tenant-a") is None
    assert mi.ms_teams_aad_tenant_owner(session, "tenant-a") == nb_tenant
    assert mi.ms_teams_aad_tenant_owner(session, "tenant-z") is None


def test_tenant_owner_counts_legacy_rows():
    import uuid

    from notifications_server.models.models import MessagingPlatform
    from notifications_server.services import messaging_installations as mi

    nb_tenant = "aaaaaaaa-1111-4111-8111-111111111111"
    session = _session_with(
        MessagingPlatform(
            id=uuid.uuid4(),
            tenant_id=uuid.UUID(nb_tenant),
            platform="ms_teams",
            team_id="object-1.tenant-b",
            client_id="c",
            token="t",
        )
    )
    assert mi.ms_teams_aad_tenant_owner(session, "tenant-b") == nb_tenant


def _run_connect(owner, tenant_id="aaaaaaaa-1111-4111-8111-111111111111"):
    from notifications_server.routers import tools

    request = SimpleNamespace(headers={"x-user-email": "admin@example.com"})
    token_result = {"id_token_claims": {"tid": "tenant-a", "preferred_username": "admin@example.com"}}
    service = mock.Mock()
    with (
        mock.patch.object(tools, "find_installation_by_tenant_and_platform", return_value=[]),
        mock.patch.object(tools, "teams_app") as teams_app,
        mock.patch.object(tools, "ms_teams_aad_tenant_owner", return_value=owner) as owner_lookup,
        mock.patch.object(tools, "MsTeamsService", return_value=service),
        mock.patch.object(tools, "Session"),
        mock.patch.object(tools, "render_success_page", return_value="<html></html>"),
    ):
        teams_app.acquire_token_by_authorization_code.return_value = token_result
        teams_app.get_accounts.return_value = [{"home_account_id": "object-1.tenant-a", "local_account_id": "object-1"}]
        result = asyncio.run(tools.ms_teams_oauth_redirect(request, {"code": "c", "tenant_id": tenant_id}))
    return result, service, owner_lookup


def test_connect_refuses_a_microsoft_tenant_linked_elsewhere():
    import pytest
    from fastapi import HTTPException

    from notifications_server.routers import tools

    with pytest.raises(HTTPException) as exc:
        _run_connect(owner="bbbbbbbb-2222-4222-8222-222222222222")
    assert exc.value.status_code == 400
    assert exc.value.detail == tools.MS_TEAMS_TENANT_ALREADY_LINKED


def test_connect_saves_when_the_microsoft_tenant_is_free_or_already_ours():
    for owner in (None, "aaaaaaaa-1111-4111-8111-111111111111"):
        _, service, owner_lookup = _run_connect(owner=owner)
        assert owner_lookup.call_args.args[1] == "tenant-a"
        service.save_teams_installation.assert_called_once()
