"""The bot's activities keep a bound team's row current and release a team the
app leaves; they never bind one."""

import asyncio
from unittest import mock

from botbuilder.schema import Activity

from notifications_server.services.ms_teams import bot_installations as bi
from notifications_server.services.ms_teams import team_binding as tb

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
            "event_time": None,
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


def test_an_unbound_team_is_never_recorded_or_bound():
    session = _session_with()
    assert bi.apply_team_change(session, _change()) == bi.NOT_CONNECTED
    released = _team_row("released")
    session = _session_with(released)
    assert bi.apply_team_change(session, _change()) == bi.NOT_CONNECTED
    integ, cfg = _team(session)
    assert (integ.status, str(integ.tenant_id), cfg) == ("released", NB_TENANT, {}), "a re-add needs a new link"


def test_a_bound_teams_fields_are_refreshed_and_bound_at_is_left_alone():
    row = _team_row()
    session = _session_with(row, _config(row.id, "bound_at", "2026-11-01T10:00:00"))
    assert bi.apply_team_change(session, _change()) == bi.RECORDED
    integ, cfg = _team(session)
    assert (integ.name, str(integ.tenant_id), integ.status) == ("group-1", NB_TENANT, "enabled")
    assert cfg == {
        "team_thread_id": "19:general@thread.tacv2",
        "team_name": "Platform",
        "service_url": SERVICE_URL,
        "aad_tenant_id": "tenant-a",
        "app_id": "app-1",
        "bound_at": "2026-11-01T10:00:00",
    }

    assert bi.apply_team_change(session, _change()) == bi.UNCHANGED, "a repeat event writes nothing"

    assert bi.apply_team_change(session, _change(team_name="Platform On-call", team_thread_id="")) == bi.RECORDED
    _, cfg = _team(session)
    assert cfg["team_name"] == "Platform On-call"
    assert cfg["team_thread_id"] == "19:general@thread.tacv2", "an empty field keeps the stored value"


def test_the_app_leaving_a_team_releases_it():
    session = _session_with(_team_row())
    with mock.patch.object(bi, "release_team", return_value=tb.RELEASED_TEAM) as release:
        assert bi.apply_team_change(session, _change("remove", event_time="t")) == bi.REMOVED
    assert release.call_args.args[1:] == ("group-1", "t")
    with mock.patch.object(bi, "release_team", return_value=tb.UNCHANGED):
        assert bi.apply_team_change(session, _change("remove")) == bi.UNCHANGED


def test_a_stored_team_name_survives_later_messages():
    session = _session_with(_team_row())
    # Teams leaves the team name out of message events.
    assert bi.apply_team_change(session, _change(team_name="", from_message=True)) == bi.RECORDED
    integ, cfg = _team(session)
    assert "team_name" not in cfg

    bi.store_team_name(session, integ.id, "Platform")
    assert _team(session)[1]["team_name"] == "Platform"
    assert bi.apply_team_change(session, _change(team_name="", from_message=True)) == bi.UNCHANGED
    assert _team(session)[1]["team_name"] == "Platform"


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
    assert tb.aad_tenant_id_from_account_id("object-1.tenant-a") == "tenant-a"
    assert tb.aad_tenant_id_from_account_id("tenant-a") == "tenant-a"
    assert tb.aad_tenant_id_from_account_id(None) == ""


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


def _team_row(status="enabled"):
    import uuid

    from notifications_server.models.models import Integration

    return Integration(
        id=uuid.uuid4(),
        tenant_id=uuid.UUID(NB_TENANT),
        type=bi.TEAM_TYPE,
        source="user",
        name="group-1",
        status=status,
        labels={},
    )


def _config(integration_id, name, value):
    import uuid

    from notifications_server.models.models import IntegrationConfigValue

    return IntegrationConfigValue(
        id=uuid.uuid4(), integration_id=integration_id, name=name, value=value, is_encrypted=False
    )
