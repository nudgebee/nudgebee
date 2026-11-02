"""Which workspace a Teams team belongs to: binding, takeover, races, release,
disconnect, and the marker that exists while a workspace has a bound team."""

import datetime
import uuid
from types import SimpleNamespace
from unittest import mock

import pytest
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session

from notifications_server.configs import settings
from notifications_server.models.models import (
    Base,
    ChannelAccountMapping,
    Integration,
    IntegrationConfigValue,
    MessagingPlatform,
)
from notifications_server.services import cache as cache_module
from notifications_server.services import messaging_installations as mi
from notifications_server.services.ms_teams import team_binding as tb

AAD = "tenant-a"
OTHER_AAD = "tenant-b"
W1 = uuid.UUID("aaaaaaaa-1111-4111-8111-111111111111")
W2 = uuid.UUID("bbbbbbbb-2222-4222-8222-222222222222")
W3 = uuid.UUID("cccccccc-3333-4333-8333-333333333333")
T1, T2 = "group-1", "group-2"


def _team(group=T1, aad=AAD):
    return {
        "team_aad_group_id": group,
        "team_thread_id": f"19:{group}@thread.tacv2",
        "team_name": group.title(),
        "service_url": "https://smba.example.com/teams/",
        "aad_tenant_id": aad,
        "app_id": "app-1",
    }


@pytest.fixture
def engine(tmp_path):
    engine = create_engine(f"sqlite:///{tmp_path / 'db.sqlite'}")
    Base.metadata.create_all(
        engine,
        tables=[
            Integration.__table__,
            IntegrationConfigValue.__table__,
            MessagingPlatform.__table__,
            ChannelAccountMapping.__table__,
        ],
    )
    with engine.begin() as conn:
        conn.execute(
            text(
                "CREATE UNIQUE INDEX integrations_ms_teams_team_name_uk ON integrations (name) "
                "WHERE type = 'ms_teams_team'"
            )
        )
    return engine


@pytest.fixture(autouse=True)
def quiet(monkeypatch):
    monkeypatch.setattr(tb, "create_audit_request", mock.Mock())
    monkeypatch.setattr(tb, "get_user_by_email_address", lambda email: None)
    monkeypatch.setattr(tb, "Cache", mock.Mock())
    monkeypatch.setattr(tb, "get_sender", mock.Mock())
    monkeypatch.setattr(settings.ms_teams, "client_id", "app-1")


@pytest.fixture
def locks(monkeypatch):
    taken = []
    monkeypatch.setattr(tb, "_lock", lambda session, workspaces: taken.append(sorted(str(w) for w in workspaces)))
    return taken


def _add(engine, *rows):
    with Session(engine) as session:
        session.add_all(rows)
        session.commit()


def _team_row(group, workspace, status="enabled", aad=AAD, bound_at=None):
    row = Integration(
        id=uuid.uuid4(), tenant_id=workspace, type=tb.TEAM_TYPE, source="user", name=group, status=status, labels={}
    )
    values = [
        IntegrationConfigValue(id=uuid.uuid4(), integration_id=row.id, name="aad_tenant_id", value=aad),
        IntegrationConfigValue(
            id=uuid.uuid4(), integration_id=row.id, name="team_thread_id", value=f"19:{group}@thread.tacv2"
        ),
    ]
    if bound_at:
        values.append(IntegrationConfigValue(id=uuid.uuid4(), integration_id=row.id, name=tb.BOUND_AT, value=bound_at))
    return [row, *values]


def _marker(workspace, aad=AAD):
    return Integration(
        id=uuid.uuid4(), tenant_id=workspace, type=tb.MARKER_TYPE, source="user", name=aad, status="enabled", labels={}
    )


def _mapping(workspace, group, channel="19:ops@thread.tacv2"):
    return ChannelAccountMapping(
        id=uuid.uuid4(),
        tenant_id=workspace,
        platform="ms_teams",
        team_id=group,
        channel_id=channel,
        account_id=uuid.uuid4(),
        channel_metadata="{}",
    )


def _state(engine):
    """{(type, name, workspace): status} and the mappings left, as plain values."""
    with Session(engine) as session:
        rows = {(i.type, i.name, i.tenant_id): i.status for i in session.query(Integration).all()}
        mappings = sorted((m.tenant_id, m.team_id) for m in session.query(ChannelAccountMapping).all())
    return rows, mappings


def _bind(engine, workspace, team=None, actor="dana@contoso.com"):
    with Session(engine) as session:
        return tb.bind_team(session, team or _team(), workspace, actor)


def _config(engine, group):
    with Session(engine) as session:
        row = session.query(Integration).filter(Integration.type == tb.TEAM_TYPE, Integration.name == group).one()
        return mi._decrypt_config(
            session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == row.id).all()
        )


# ------------------------------- binding -------------------------------


def test_a_bind_records_the_team_and_the_workspaces_marker(engine, locks):
    assert _bind(engine, W1) == tb.CONNECTED
    rows, _ = _state(engine)
    assert rows == {(tb.TEAM_TYPE, T1, W1): "enabled", (tb.MARKER_TYPE, AAD, W1): "enabled"}
    assert tb.BOUND_AT in _config(engine, T1)
    assert locks == [[str(W1)]]


def test_two_teams_of_one_microsoft_tenant_go_to_two_workspaces(engine, locks):
    assert _bind(engine, W1) == tb.CONNECTED
    assert _bind(engine, W2, _team(T2)) == tb.CONNECTED
    rows, _ = _state(engine)
    assert rows == {
        (tb.TEAM_TYPE, T1, W1): "enabled",
        (tb.TEAM_TYPE, T2, W2): "enabled",
        (tb.MARKER_TYPE, AAD, W1): "enabled",
        (tb.MARKER_TYPE, AAD, W2): "enabled",
    }


def test_a_second_team_of_a_workspace_adds_no_marker(engine, locks):
    _bind(engine, W1)
    assert _bind(engine, W1, _team(T2)) == tb.CONNECTED
    rows, _ = _state(engine)
    assert [k for k in rows if k[0] == tb.MARKER_TYPE] == [(tb.MARKER_TYPE, AAD, W1)]


def test_a_team_bound_elsewhere_is_refused_and_nothing_changes(engine, locks):
    _add(engine, *_team_row(T1, W1), _marker(W1), _mapping(W1, T1))
    before = _state(engine)
    assert _bind(engine, W2) == tb.TEAM_LINKED_ELSEWHERE
    assert _state(engine) == before


def test_a_team_already_bound_here_says_so(engine, locks):
    _add(engine, *_team_row(T1, W1), _marker(W1))
    assert _bind(engine, W1) == tb.ALREADY_CONNECTED
    assert locks == []


def test_a_workspace_of_another_microsoft_tenant_is_refused_and_rolled_back(engine, locks):
    _add(engine, _marker(W1, aad=OTHER_AAD))
    assert _bind(engine, W1) == tb.OTHER_MICROSOFT_TENANT
    rows, _ = _state(engine)
    assert rows == {(tb.MARKER_TYPE, OTHER_AAD, W1): "enabled"}, "the claimed team is rolled back too"


def test_a_marker_named_the_old_way_still_matches(engine, locks):
    _add(engine, _marker(W1, aad=f"object-1.{AAD}"))
    assert _bind(engine, W1) == tb.CONNECTED


def test_a_takeover_deletes_the_previous_workspaces_mappings_for_that_team(engine, locks):
    _add(
        engine,
        *_team_row(T1, W1, status="released"),
        *_team_row(T2, W1),
        _marker(W1),
        _mapping(W1, T1),
        _mapping(W1, T2),
        _mapping(W2, "group-9"),
    )
    assert _bind(engine, W2) == tb.CONNECTED
    rows, mappings = _state(engine)
    assert rows[(tb.TEAM_TYPE, T1, W2)] == "enabled"
    assert (tb.MARKER_TYPE, AAD, W1) in rows, "W1 still has T2 bound"
    assert mappings == sorted([(W1, T2), (W2, "group-9")])
    assert locks == [sorted([str(W1), str(W2)])], "both workspaces are locked, in order"


def test_a_takeover_drops_the_previous_workspaces_marker_when_it_has_no_team_left(engine, locks):
    _add(engine, *_team_row(T1, W1, status="released"), _marker(W1))
    assert _bind(engine, W2) == tb.CONNECTED
    rows, _ = _state(engine)
    assert (tb.MARKER_TYPE, AAD, W1) not in rows


def test_a_relink_by_the_same_workspace_keeps_its_mappings(engine, locks):
    _add(engine, *_team_row(T1, W1, status="released"), _mapping(W1, T1))
    assert _bind(engine, W1) == tb.CONNECTED
    rows, mappings = _state(engine)
    assert rows[(tb.TEAM_TYPE, T1, W1)] == "enabled"
    assert mappings == [(W1, T1)]


def test_a_bind_deletes_every_other_workspaces_mappings_of_the_team(engine, locks):
    _add(engine, _mapping(W2, T1), _mapping(W3, T1, channel="19:x"), _mapping(W1, T1, channel="19:y"))
    assert _bind(engine, W1) == tb.CONNECTED
    _, mappings = _state(engine)
    assert mappings == [(W1, T1)], "a squatted mapping never survives the bind"


def test_a_takeover_tells_the_team_and_audits_only_in_the_workspace_that_lost_it(engine, locks, monkeypatch):
    _add(engine, *_team_row(T1, W1, status="released"))
    monkeypatch.setattr(tb, "get_user_by_email_address", lambda email: {"id": None, "display_name": "Dana Lee"})
    monkeypatch.setattr(tb, "_workspace_name", lambda session, workspace: "Ops West")
    assert _bind(engine, W2) == tb.CONNECTED

    send = tb.get_sender.return_value.send
    service_url, conversation_id, activity = send.call_args.args
    assert (service_url, conversation_id) == (_team()["service_url"], _team()["team_thread_id"])
    assert activity.text == "This team is now connected to Ops West, connected by Dana Lee."

    audits = [c.kwargs for c in tb.create_audit_request.call_args_list]
    lost = [a for a in audits if a["tenant_id"] == str(W1)]
    assert len(lost) == 1
    audit = lost[0]
    assert audit["event_type"] == "NOTIFICATION_MS_TEAMS_CONFIGURATION_DELETE"
    assert audit["event_state"] == {"status": "disconnected", "team": "Group-1"}
    assert audit["user_id"] is None
    assert str(W2) not in repr(audit) and "Ops West" not in repr(audit) and "Dana" not in repr(audit)


def test_a_failed_announcement_doesnt_undo_the_takeover(engine, locks):
    _add(engine, *_team_row(T1, W1, status="released"))
    tb.get_sender.return_value.send.side_effect = RuntimeError("Teams down")
    assert _bind(engine, W2) == tb.CONNECTED
    rows, _ = _state(engine)
    assert rows[(tb.TEAM_TYPE, T1, W2)] == "enabled"


def test_every_connect_is_announced_but_only_a_takeover_is_audited_elsewhere(engine, locks, monkeypatch):
    monkeypatch.setattr(tb, "get_user_by_email_address", lambda email: {"id": None, "display_name": "Dana Lee"})
    monkeypatch.setattr(tb, "_workspace_name", lambda session, workspace: "Ops West")
    assert _bind(engine, W2, _team(T2)) == tb.CONNECTED
    activity = tb.get_sender.return_value.send.call_args.args[2]
    assert activity.text == "This team is now connected to Ops West, connected by Dana Lee."
    assert all(c.kwargs["tenant_id"] == str(W2) for c in tb.create_audit_request.call_args_list)


def test_a_refused_connect_is_not_announced(engine, locks):
    _add(engine, *_team_row(T1, W1))
    assert _bind(engine, W2) == tb.TEAM_LINKED_ELSEWHERE
    tb.get_sender.return_value.send.assert_not_called()


# ------------------------------- races -------------------------------


def test_two_workspaces_claiming_an_unbound_team_at_once_one_wins(engine):
    def other_wins(session, workspaces):
        # The other redeem commits between this one's read and its claim.
        _add(engine, *_team_row(T1, W2), _marker(W2))

    with mock.patch.object(tb, "_lock", side_effect=other_wins):
        assert _bind(engine, W1) == tb.TEAM_LINKED_ELSEWHERE
    rows, _ = _state(engine)
    assert rows == {(tb.TEAM_TYPE, T1, W2): "enabled", (tb.MARKER_TYPE, AAD, W2): "enabled"}, "no marker for W1"


def test_the_same_workspace_binding_an_unbound_team_twice_at_once_says_already_connected(engine):
    def same_workspace_wins(session, workspaces):
        _add(engine, *_team_row(T1, W1), _marker(W1))

    with mock.patch.object(tb, "_lock", side_effect=same_workspace_wins):
        assert _bind(engine, W1) == tb.ALREADY_CONNECTED
    rows, _ = _state(engine)
    assert rows == {(tb.TEAM_TYPE, T1, W1): "enabled", (tb.MARKER_TYPE, AAD, W1): "enabled"}


def test_a_released_team_bound_by_someone_else_first_is_refused(engine):
    _add(engine, *_team_row(T1, W3, status="released"))

    def other_wins(session, workspaces):
        with Session(engine) as other:
            row = other.query(Integration).filter(Integration.name == T1).one()
            row.tenant_id, row.status = W2, "enabled"
            other.commit()

    with mock.patch.object(tb, "_lock", side_effect=other_wins):
        assert _bind(engine, W1) == tb.TEAM_LINKED_ELSEWHERE
    rows, _ = _state(engine)
    assert rows == {(tb.TEAM_TYPE, T1, W2): "enabled"}


def test_the_lock_is_taken_per_workspace_in_sorted_order():
    session = mock.Mock()
    tb._lock(session, [W2, W1, W2])
    keys = [c.args[1]["key"] for c in session.execute.call_args_list]
    assert keys == [f"ms_teams:{W1}", f"ms_teams:{W2}"]
    assert "pg_advisory_xact_lock" in str(session.execute.call_args_list[0].args[0])


# ------------------------------- release -------------------------------


def _release(engine, group, event_time):
    with Session(engine) as session:
        return tb.release_team(session, group, event_time)


def test_removing_the_app_releases_the_team_and_its_workspaces_last_marker(engine, locks):
    _add(engine, *_team_row(T1, W1, bound_at="2026-11-01T10:00:00"), _marker(W1), _mapping(W1, T1))
    assert _release(engine, T1, datetime.datetime(2026, 11, 2, tzinfo=datetime.timezone.utc)) == tb.RELEASED_TEAM
    rows, mappings = _state(engine)
    assert rows == {(tb.TEAM_TYPE, T1, W1): "released"}
    assert mappings == [(W1, T1)], "kept for a re-link by the same workspace"


def test_a_release_records_when_and_a_link_minted_before_it_cant_bind(engine, locks):
    _add(engine, *_team_row(T1, W1))
    before = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(seconds=5)
    assert _release(engine, T1, None) == tb.RELEASED_TEAM
    assert tb.RELEASED_AT in _config(engine, T1)
    with Session(engine) as session:
        assert tb.bind_team(session, _team(), W2, "dana@contoso.com", minted_at=before) == tb.LINK_EXPIRED
    after = datetime.datetime.now(datetime.timezone.utc) + datetime.timedelta(seconds=5)
    with Session(engine) as session:
        assert tb.bind_team(session, _team(), W2, "dana@contoso.com", minted_at=after) == tb.CONNECTED


def test_the_marker_stays_while_another_team_is_bound(engine, locks):
    _add(engine, *_team_row(T1, W1), *_team_row(T2, W1), _marker(W1))
    assert _release(engine, T1, None) == tb.RELEASED_TEAM
    rows, _ = _state(engine)
    assert rows[(tb.MARKER_TYPE, AAD, W1)] == "enabled"


def test_a_removal_older_than_the_bind_is_ignored(engine, locks):
    _add(engine, *_team_row(T1, W1, bound_at="2026-11-02T10:00:00"), _marker(W1))
    late = datetime.datetime(2026, 11, 2, 9, 59, tzinfo=datetime.timezone.utc)
    assert _release(engine, T1, late) == tb.UNCHANGED
    rows, _ = _state(engine)
    assert rows[(tb.TEAM_TYPE, T1, W1)] == "enabled" and (tb.MARKER_TYPE, AAD, W1) in rows


def test_releasing_an_unbound_team_changes_nothing(engine, locks):
    _add(engine, *_team_row(T1, W1, status="released"))
    assert _release(engine, T1, None) == tb.UNCHANGED
    assert _release(engine, "group-unknown", None) == tb.UNCHANGED
    assert locks == []


# ------------------------------- disconnect -------------------------------


def test_disconnect_releases_every_team_and_removes_the_connection(engine, locks):
    _add(
        engine,
        *_team_row(T1, W2),
        *_team_row(T2, W1),
        _marker(W2),
        _marker(W1),
        _mapping(W2, T1),
        _mapping(W1, T2),
    )
    with Session(engine) as session:
        assert tb.disconnect_workspace(session, W2) == 1
    assert tb.RELEASED_AT in _config(engine, T1)
    rows, mappings = _state(engine)
    assert rows == {
        (tb.TEAM_TYPE, T1, W2): "released",
        (tb.TEAM_TYPE, T2, W1): "enabled",
        (tb.MARKER_TYPE, AAD, W1): "enabled",
    }
    assert mappings == [(W1, T2)]
    with Session(engine) as session:
        assert tb.team_owner(session, T1) is None, "a message in T1 now gets the not-connected reply"


# ------------------------------- who a conversation belongs to -------------------------------


def _activity(conversation_type, group=T1, aad=AAD):
    channel_data = {"tenant": {"id": aad}}
    if conversation_type == "channel":
        channel_data["team"] = {"aadGroupId": group}
    return SimpleNamespace(conversation=SimpleNamespace(conversation_type=conversation_type), channel_data=channel_data)


def test_a_channel_belongs_to_its_teams_owner_and_a_chat_to_every_eligible_workspace(engine):
    _add(
        engine,
        *_team_row(T1, W1),
        *_team_row(T2, W2),
        *_team_row("group-3", W3, status="released"),
        *_team_row("group-4", W3, aad=OTHER_AAD),
    )
    with Session(engine) as session:
        assert tb.workspaces_for(session, _activity("channel", T1)) == {str(W1)}
        assert tb.workspaces_for(session, _activity("channel", T2)) == {str(W2)}
        assert tb.workspaces_for(session, _activity("channel", "group-3")) == set(), "released is unbound"
        assert tb.workspaces_for(session, _activity("personal")) == {str(W1), str(W2)}
        assert tb.workspaces_for(session, _activity("groupChat", aad=OTHER_AAD)) == {str(W3)}
        assert tb.workspaces_for(session, _activity("personal", aad="")) == set()


def test_team_data_decides_a_team_conversation_whatever_its_type_says(engine):
    _add(engine, *_team_row(T1, W1), *_team_row(T2, W2))

    def activity(conversation_type, team=None):
        channel_data = {"tenant": {"id": AAD}}
        if team is not None:
            channel_data["team"] = team
        return SimpleNamespace(
            conversation=SimpleNamespace(conversation_type=conversation_type), channel_data=channel_data
        )

    with Session(engine) as session:
        assert tb.workspaces_for(session, activity(None, {"aadGroupId": T1})) == {str(W1)}
        assert tb.workspaces_for(session, activity("groupChat", {"aadGroupId": T1})) == {str(W1)}
        assert tb.workspaces_for(session, activity("personal", {"id": "19:x@thread.tacv2"})) == set()
        assert tb.workspaces_for(session, activity("channel")) == set()
        assert tb.workspaces_for(session, activity(None)) == set()
        assert tb.workspaces_for(session, activity("groupChat")) == {str(W1), str(W2)}


def test_a_card_click_in_a_channel_belongs_to_its_teams_owner_by_thread_id(engine):
    _add(engine, *_team_row(T1, W1), *_team_row("group-3", W2, status="released"))

    def click(thread_id):
        return SimpleNamespace(
            conversation=SimpleNamespace(conversation_type="channel"),
            channel_data={"tenant": {"id": AAD}, "team": {"id": thread_id}},
        )

    with Session(engine) as session:
        assert tb.workspaces_for(session, click(f"19:{T1}@thread.tacv2")) == {str(W1)}
        assert tb.workspaces_for(session, click("19:group-3@thread.tacv2")) == set(), "released is unbound"
        assert tb.workspaces_for(session, click("19:unknown@thread.tacv2")) == set()


def test_a_thread_id_bound_in_two_workspaces_resolves_to_neither(engine):
    twin = _team_row("group-9", W2)
    twin[2].value = f"19:{T1}@thread.tacv2"
    _add(engine, *_team_row(T1, W1), *twin)
    click = SimpleNamespace(
        conversation=SimpleNamespace(conversation_type="channel"),
        channel_data={"tenant": {"id": AAD}, "team": {"id": f"19:{T1}@thread.tacv2"}},
    )
    with Session(engine) as session:
        assert tb.workspaces_for(session, click) == set()


# ------------------------------- @mention links -------------------------------


def test_one_connect_link_per_person_per_team_until_the_claim_expires():
    store = {}

    def set_(key, value, nx=False, ex=None):
        if nx and key in store:
            return None
        store[key] = (value, ex)
        return True

    cache = cache_module.Cache.__new__(cache_module.Cache)
    cache.redis_client = SimpleNamespace(set=set_)
    with mock.patch.object(cache_module.Cache, "_ensure_connection", return_value=True):
        assert cache.claim_teams_connect_link(T1, "person-1", 300) is True
        assert cache.claim_teams_connect_link(T1, "person-1", 300) is False
        assert cache.claim_teams_connect_link(T1, "person-2", 300) is True
        assert cache.claim_teams_connect_link(T2, "person-1", 300) is True
    assert store[f"teams_connect_link:{T1}:person-1"] == ("1", 300)


# ------------------------------- the disconnect endpoint -------------------------------


def test_the_disconnect_endpoint_disconnects_the_callers_workspace():
    import asyncio
    import json

    from notifications_server.routers import common as routes
    from notifications_server.routers.common import verify_action_token

    route = next(r for r in routes.router.routes if r.path == "/api/integrations/ms-teams/disconnect")
    assert [d.dependency for d in route.dependencies] == [verify_action_token]
    request = SimpleNamespace(headers={})
    with (
        mock.patch.object(routes, "Session"),
        mock.patch.object(routes, "disconnect_workspace", return_value=2) as disconnect,
    ):
        response = asyncio.run(routes.disconnect_ms_teams(request, {"session_variables": {"tenant_id": str(W1)}}))
        assert json.loads(response.body) == {"success": True, "released_teams": 2}
        assert disconnect.call_args.args[1] == str(W1)
        refused = asyncio.run(routes.disconnect_ms_teams(request, {"session_variables": {}}))
    assert json.loads(refused.body)["success"] is False
    assert disconnect.call_count == 1


def test_a_chat_whose_tenant_is_not_an_object_acts_in_no_workspace():
    activity = SimpleNamespace(conversation=SimpleNamespace(conversation_type="personal"), channel_data={"tenant": "x"})
    assert tb.workspaces_for(mock.Mock(), activity) == set()


# ------------------------------- legacy installs -------------------------------


def test_a_legacy_teams_row_is_never_an_install(engine):
    legacy = MessagingPlatform(id=uuid.uuid4(), tenant_id=W1, platform="ms_teams", client_id="c", token="t")
    slack = MessagingPlatform(id=uuid.uuid4(), tenant_id=W1, platform="slack", client_id="c", token="t")
    _add(engine, legacy, slack)
    from notifications_server.repositories.oauth_repository import find_installation_by_tenant_and_platform

    with Session(engine) as session:
        assert mi.load_installations(session, W1, "ms_teams") == []
        assert mi.load_installation(session, W1, "ms_teams") is None
        assert [i.platform for i in mi.load_installations(session, W1, "slack")] == ["slack"]
        assert find_installation_by_tenant_and_platform(session, W1, "ms_teams") == []
        assert len(find_installation_by_tenant_and_platform(session, W1, "slack")) == 1


# ------------------------------- Bot Connector hosts -------------------------------


@pytest.mark.parametrize(
    "url, trusted",
    [
        ("https://smba.trafficmanager.net/amer/", True),
        ("https://smba.infra.gcc.teams.microsoft.com/teams/", True),
        ("https://smba.infra.gov.teams.microsoft.us/", True),
        ("https://smba.infra.dod.teams.microsoft.us/", True),
        ("https://SMBA.trafficmanager.net/emea/", True),
        ("http://smba.trafficmanager.net/amer/", False),
        ("https://smba.trafficmanager.net.example.com/", False),
        ("https://evil.example.com/smba.trafficmanager.net/", False),
        ("https://smba.trafficmanager.net:8443/", False),
        ("https://smba.trafficmanager.net:notaport/", False),
        ("", False),
        (None, False),
    ],
)
def test_only_bot_connector_hosts_are_trusted(url, trusted):
    from notifications_server.services.ms_teams import bot_sender as bs

    assert bs.is_bot_connector_url(url) is trusted


def test_a_stored_service_url_outside_the_bot_connector_never_gets_the_token():
    from notifications_server.services.ms_teams import bot_sender as bs

    sender = bs.TeamsBotSender.__new__(bs.TeamsBotSender)
    sender._credentials, sender._channel_cache = mock.Mock(), {}
    with mock.patch.object(bs.MicrosoftAppCredentials, "trust_service_url") as trust:
        with pytest.raises(bs.TeamsBotError):
            sender.send("https://attacker.example.com/", "19:ops", mock.Mock())
        with pytest.raises(bs.TeamsBotError):
            sender.list_channels(bs.TeamRef(None, None, T1, "19:t", "", "https://attacker.example.com/"))
    trust.assert_not_called()


def test_a_legacy_teams_row_is_never_sent_to():
    import asyncio

    from notifications_server.services.message import MessageService

    rows = [
        MessagingPlatform(id=uuid.uuid4(), tenant_id=W1, platform=p, client_id="c", token="t")
        for p in ("ms_teams", "slack", "google_chat")
    ]
    result = mock.Mock()
    result.scalars.return_value.all.return_value = rows
    session = mock.Mock(execute=mock.AsyncMock(return_value=result))
    installs = asyncio.run(MessageService.get_installed_platforms(session))
    assert [i.platform for i in installs] == ["slack"]


def test_a_cached_reply_outside_the_bot_connector_is_not_sent():
    import asyncio

    from notifications_server.services import common as common_module
    from notifications_server.services.common import CommonService

    service = CommonService.__new__(CommonService)
    service.teams_adapter = mock.Mock(continue_conversation=mock.AsyncMock())
    service.teams_app_id = "app-1"
    ref = {"conversation_id": "19:ops", "service_url": "https://attacker.example.com/", "bot_id": "28:b"}
    with mock.patch.object(common_module.MicrosoftAppCredentials, "trust_service_url") as trust:
        assert asyncio.run(service.teams_reply_from_conversation_reference(ref, "hi", "aad")) is False
        assert asyncio.run(service.teams_reply_with_card_from_conversation_reference(ref, {}, "aad")) is False
    trust.assert_not_called()
    service.teams_adapter.continue_conversation.assert_not_called()
