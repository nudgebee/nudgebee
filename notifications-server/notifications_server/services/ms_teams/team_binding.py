"""Which workspace each Microsoft Teams team belongs to.

A team row (ms_teams_team, named by the team's Entra group id) with status
'enabled' is bound to its tenant_id and has the app in it; any other status means
unbound, and its tenant_id is only history. Only bind_team writes 'enabled'. A
workspace's ms_teams row (the marker: its Microsoft tenant id and app) exists
exactly while the workspace has an enabled team. bind_team, release_team and
disconnect_workspace are the only writers of both, each one transaction under a
per-workspace advisory lock."""

import logging
import uuid
from datetime import datetime, timezone
from typing import Any, Dict, Iterable, Optional, Set

from botbuilder.schema import Activity, ActivityTypes
from sqlalchemy import or_, text, update
from sqlalchemy.exc import IntegrityError

from notifications_server.configs.settings import settings
from notifications_server.models.enums import EventAction, EventActor, EventCategory, EventStatus, EventType
from notifications_server.models.models import ChannelAccountMapping, Integration, IntegrationConfigValue
from notifications_server.services.audit import create_audit_request
from notifications_server.services.cache import Cache
from notifications_server.services.messaging_installations import _decrypt_config, _write_config_values
from notifications_server.services.ms_teams.activity import TEAM_TYPE, channel_data_of, in_chat, in_team, team_of
from notifications_server.services.ms_teams.bot_sender import DEFAULT_SERVICE_URL, get_sender
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_by_email_address

LOG = logging.getLogger(__name__)

MARKER_TYPE = "ms_teams"
ENABLED, RELEASED = "enabled", "released"
TEAM_CONFIG_KEYS = ("team_thread_id", "team_name", "service_url", "aad_tenant_id", "app_id")
# When bind_team last bound the team. Nothing else writes it.
BOUND_AT = "bound_at"
# When the team was last released. A link minted before it can't bind the team.
RELEASED_AT = "released_at"

CONNECTED = "connected"
ALREADY_CONNECTED = "already_connected"
TEAM_LINKED_ELSEWHERE = "team_linked_elsewhere"
OTHER_MICROSOFT_TENANT = "other_microsoft_tenant"
LINK_EXPIRED = "expired"
RELEASED_TEAM, UNCHANGED = "released", "unchanged"


def aad_tenant_id_from_account_id(account_id: Optional[str]) -> str:
    """Microsoft tenant id that names an ms_teams integration. Older installs
    stored the MSAL home_account_id ("<object id>.<tenant id>")."""
    account_id = (account_id or "").strip()
    _, sep, tenant = account_id.partition(".")
    return tenant if sep and tenant else account_id


def team_owner(session, aad_group_id) -> Optional[str]:
    """The workspace a team is bound to, or None."""
    if not aad_group_id:
        return None
    row = (
        session.query(Integration.tenant_id)
        .filter(Integration.type == TEAM_TYPE, Integration.name == aad_group_id, Integration.status == ENABLED)
        .first()
    )
    return str(row[0]) if row else None


def team_owner_by_thread(session, thread_id) -> Optional[str]:
    """The workspace a team is bound to, found by the team's thread id: card
    clicks in a channel carry the team's id but not its aadGroupId. None when
    the id matches no bound team, or more than one workspace."""
    if not thread_id:
        return None
    rows = (
        session.query(Integration.tenant_id)
        .join(IntegrationConfigValue, IntegrationConfigValue.integration_id == Integration.id)
        .filter(
            Integration.type == TEAM_TYPE,
            Integration.status == ENABLED,
            IntegrationConfigValue.name == "team_thread_id",
            IntegrationConfigValue.value == thread_id,
        )
        .distinct()
        .limit(2)
        .all()
    )
    return str(rows[0][0]) if len(rows) == 1 else None


def eligible(session, aad_tenant_id) -> Set[str]:
    """The workspaces with a team bound in this Microsoft tenant."""
    if not aad_tenant_id:
        return set()
    rows = (
        session.query(Integration.tenant_id)
        .join(IntegrationConfigValue, IntegrationConfigValue.integration_id == Integration.id)
        .filter(
            Integration.type == TEAM_TYPE,
            Integration.status == ENABLED,
            IntegrationConfigValue.name == "aad_tenant_id",
            IntegrationConfigValue.value == aad_tenant_id,
        )
        .distinct()
        .all()
    )
    return {str(row[0]) for row in rows}


def workspaces_for(session, activity) -> Set[str]:
    """The workspaces a conversation may act in: in a team, the team's owner; in
    a personal or group chat, every workspace eligible for the conversation's
    Microsoft tenant; anywhere else, none."""
    if in_team(activity):
        team = team_of(activity)
        if team.get("aadGroupId"):
            owner = team_owner(session, team["aadGroupId"])
        else:
            owner = team_owner_by_thread(session, team.get("id"))
        return {owner} if owner else set()
    if in_chat(activity):
        tenant = channel_data_of(activity).get("tenant")
        return eligible(session, tenant.get("id") if isinstance(tenant, dict) else None)
    return set()


def _lock(session, workspaces: Iterable[Any]) -> None:
    """Serialise team lifecycle writes per workspace, until the transaction ends.
    Sorted, so two transactions taking the same pair can't deadlock."""
    for workspace in sorted({str(w) for w in workspaces}):
        session.execute(text("SELECT pg_advisory_xact_lock(hashtext(:key))"), {"key": f"ms_teams:{workspace}"})


def _forget_installs(workspaces: Iterable[Any]) -> None:
    for workspace in {str(w) for w in workspaces}:
        try:
            Cache().delete_cached_installations(workspace)
        except Exception as e:
            LOG.warning("Clearing cached installations for tenant %s failed: %s", workspace, e)


def _team_row(session, aad_group_id) -> Optional[Integration]:
    return (
        session.query(Integration)
        .filter(Integration.type == TEAM_TYPE, Integration.name == aad_group_id)
        .populate_existing()
        .first()
    )


def _markers(session, workspace):
    return session.query(Integration).filter(Integration.type == MARKER_TYPE, Integration.tenant_id == workspace)


def _drop_markers(session, workspace) -> None:
    ids = [row.id for row in _markers(session, workspace).all()]
    if not ids:
        return
    session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id.in_(ids)).delete(
        synchronize_session=False
    )
    session.query(Integration).filter(Integration.id.in_(ids)).delete(synchronize_session=False)


def _drop_markers_if_unused(session, workspace) -> None:
    enabled = (
        session.query(Integration.id)
        .filter(Integration.type == TEAM_TYPE, Integration.tenant_id == workspace, Integration.status == ENABLED)
        .first()
    )
    if enabled is None:
        _drop_markers(session, workspace)


def _ensure_marker(session, workspace, aad_tenant_id, actor, user_id) -> Optional[str]:
    """Create the workspace's marker if it has none; a refusal code when its
    marker names another Microsoft tenant."""
    marker = _markers(session, workspace).first()
    created = False
    if marker is None:
        now = utc_now()
        try:
            with session.begin_nested():
                marker = Integration(
                    id=uuid.uuid4(),
                    tenant_id=workspace,
                    type=MARKER_TYPE,
                    source="user",
                    name=aad_tenant_id,
                    status=ENABLED,
                    labels={},
                    created_at=now,
                    updated_at=now,
                    created_by=user_id,
                    updated_by=user_id,
                )
                session.add(marker)
            created = True
        except IntegrityError:
            marker = _markers(session, workspace).first()
            if marker is None:
                raise
    if aad_tenant_id_from_account_id(marker.name) != aad_tenant_id:
        return OTHER_MICROSOFT_TENANT
    if marker.status != ENABLED:
        marker.status = ENABLED
    if created:
        _write_config_values(session, marker.id, {"app_id": settings.ms_teams.client_id, "installed_by": actor})
    return None


def _released_at(session, integration_id) -> Optional[datetime]:
    rows = (
        session.query(IntegrationConfigValue)
        .filter(IntegrationConfigValue.integration_id == integration_id, IntegrationConfigValue.name == RELEASED_AT)
        .all()
    )
    value = _decrypt_config(rows).get(RELEASED_AT)
    try:
        return _naive_utc(datetime.fromisoformat(value)) if value else None
    except ValueError:
        return None


def _workspace_name(session, workspace) -> Optional[str]:
    try:
        row = session.execute(text("SELECT name FROM tenant WHERE id = :id"), {"id": str(workspace)}).first()
        return row[0] if row and row[0] else None
    except Exception as e:
        session.rollback()
        LOG.warning("Reading the name of workspace %s failed: %s", workspace, e)
        return None


def _announce_connected(session, team: Dict[str, Any], workspace, person: str) -> None:
    """Say in the team's General channel which workspace it is now connected
    to, and who connected it. Best effort: the bind already stands."""
    name = _workspace_name(session, workspace) or "another workspace"
    activity = Activity(
        type=ActivityTypes.message, text=f"This team is now connected to {name}, connected by {person}."
    )
    try:
        get_sender().send(team.get("service_url") or DEFAULT_SERVICE_URL, team["team_thread_id"], activity)
    except Exception as e:
        LOG.warning("Announcing the new workspace of Teams team %s failed: %s", team["team_aad_group_id"], e)


def _tell_previous_workspace(previous, team: Dict[str, Any]) -> None:
    """Audit, in the workspace that lost the team, that it now belongs to another. It
    names neither the workspace that took it nor who did."""
    team_name = team.get("team_name") or team["team_aad_group_id"]
    try:
        create_audit_request(
            user_id=None,
            tenant_id=str(previous),
            account_id=None,
            event_time=utc_now(),
            event_category=EventCategory.INTEGRATIONS.value,
            event_type=EventType.NOTIFICATION_MSTEAMS_CONFIGURATION_DELETE.value,
            event_prev_state={"team": team_name},
            event_state={"status": "disconnected", "team": team_name},
            event_actor=EventActor.MS_TEAMS.value,
            event_target="notification_configuration",
            event_action=EventAction.DELETE.value,
            event_status=EventStatus.SUCCESS.value,
            event_attr={
                "team": team_name,
                "team_id": team["team_aad_group_id"],
                "reason": f'Microsoft Teams team "{team_name}" is now connected to another workspace',
            },
        )
    except Exception as e:
        LOG.warning("Auditing the disconnect of Teams team %s failed: %s", team["team_aad_group_id"], e)


def bind_team(
    session, team: Dict[str, Any], workspace, actor: str, audit_attr=None, minted_at: Optional[datetime] = None
) -> str:
    """Bind a team to a workspace, as actor (the admin who redeemed the link
    minted at minted_at). A team bound to another workspace is refused, and so
    is a link minted before the team was last released; an unbound team is
    taken over, and the channel mappings its previous workspace had for it are
    deleted. Any refusal rolls the whole transaction back. The caller owns the
    session.

    Returns CONNECTED, ALREADY_CONNECTED, TEAM_LINKED_ELSEWHERE,
    OTHER_MICROSOFT_TENANT or LINK_EXPIRED."""
    workspace = uuid.UUID(str(workspace))
    group_id = team["team_aad_group_id"]
    aad_tenant_id = team["aad_tenant_id"]
    user = get_user_by_email_address(actor) or {}
    user_id = user.get("id")

    row = _team_row(session, group_id)
    if row is not None and row.status == ENABLED and row.tenant_id == workspace:
        session.rollback()
        return ALREADY_CONNECTED
    previous = row.tenant_id if row is not None else None
    _lock(session, {workspace} | ({previous} if previous else set()))
    # Read the release time under the lock, so a release that lands between the
    # first read and the lock still expires links minted before it.
    if row is not None and minted_at is not None:
        released_at = _released_at(session, row.id)
        if released_at is not None and _naive_utc(minted_at) < released_at:
            session.rollback()
            return LINK_EXPIRED

    now = utc_now()
    try:
        if row is None:
            session.add(
                Integration(
                    id=uuid.uuid4(),
                    tenant_id=workspace,
                    type=TEAM_TYPE,
                    source="user",
                    name=group_id,
                    status=ENABLED,
                    labels={},
                    created_at=now,
                    updated_at=now,
                    created_by=user_id,
                    updated_by=user_id,
                )
            )
            session.flush()
        else:
            # The previous owner is part of the condition: if the row changed hands
            # since it was read, the locks taken are not the right ones.
            claimed = session.execute(
                update(Integration)
                .where(
                    Integration.id == row.id,
                    Integration.tenant_id == previous,
                    or_(
                        Integration.status.is_(None), Integration.status != ENABLED, Integration.tenant_id == workspace
                    ),
                )
                .values(tenant_id=workspace, status=ENABLED, updated_at=now, updated_by=user_id)
                .execution_options(synchronize_session=False)
            ).rowcount
            if not claimed:
                session.rollback()
                return TEAM_LINKED_ELSEWHERE
    except IntegrityError as e:
        session.rollback()
        winner = _team_row(session, group_id)
        session.rollback()
        if winner is not None and winner.status == ENABLED and winner.tenant_id == workspace:
            return ALREADY_CONNECTED
        LOG.info("Teams team %s was claimed by another workspace first: %s", group_id, e.orig)
        return TEAM_LINKED_ELSEWHERE
    refusal = _ensure_marker(session, workspace, aad_tenant_id, actor, user_id)
    if refusal:
        session.rollback()
        return refusal

    team_id = _team_row(session, group_id).id
    _write_config_values(
        session,
        team_id,
        {**{key: team.get(key) or None for key in TEAM_CONFIG_KEYS}, BOUND_AT: now.isoformat()},
    )
    # Only the team's workspace may map its channels.
    session.query(ChannelAccountMapping).filter(
        ChannelAccountMapping.tenant_id != workspace,
        ChannelAccountMapping.platform == MARKER_TYPE,
        ChannelAccountMapping.team_id == group_id,
    ).delete(synchronize_session=False)
    taken_over = previous is not None and previous != workspace
    if taken_over:
        _drop_markers_if_unused(session, previous)
    session.commit()
    _forget_installs({workspace} | ({previous} if previous else set()))
    if taken_over:
        _tell_previous_workspace(previous, team)
    _announce_connected(session, team, workspace, user.get("display_name") or actor)

    try:
        create_audit_request(
            user_id=user_id,
            tenant_id=str(workspace),
            account_id=None,
            event_time=utc_now(),
            event_category=EventCategory.INTEGRATIONS.value,
            event_type=EventType.NOTIFICATION_MSTEAMS_CONFIGURATION_CREATE.value,
            event_prev_state=None,
            event_state={"status": "success"},
            event_actor=EventActor.MS_TEAMS.value,
            event_target="notification_configuration",
            event_action=EventAction.CREATE.value,
            event_status=EventStatus.SUCCESS.value,
            event_attr={"status": "success", "team": group_id, **(audit_attr or {})},
        )
    except Exception as e:
        LOG.warning("Auditing the bind of Teams team %s failed: %s", group_id, e)
    return CONNECTED


def _naive_utc(value: Optional[datetime]) -> Optional[datetime]:
    if value is not None and value.tzinfo is not None:
        value = value.astimezone(timezone.utc).replace(tzinfo=None)
    return value


def release_team(session, aad_group_id, event_time: Optional[datetime] = None) -> str:
    """The app left a team (removed, or the team deleted or archived): unbind it,
    and drop its workspace's marker if that was its last bound team. An event
    older than the team's latest bind is ignored, so a late or retried removal
    can't undo a newer bind. Channel mappings stay, for a re-link by the same
    workspace. Returns RELEASED_TEAM or UNCHANGED."""
    row = _team_row(session, aad_group_id)
    if row is None or row.status != ENABLED:
        session.rollback()
        return UNCHANGED
    workspace = row.tenant_id
    _lock(session, {workspace})
    row = _team_row(session, aad_group_id)
    if row is None or row.status != ENABLED or row.tenant_id != workspace:
        session.rollback()
        return UNCHANGED
    rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == row.id).all()
    bound_at = _decrypt_config(rows).get(BOUND_AT)
    event_time = _naive_utc(event_time) or utc_now()
    if bound_at:
        try:
            if _naive_utc(datetime.fromisoformat(bound_at)) > event_time:
                session.rollback()
                LOG.info("Ignoring a removal of Teams team %s older than its bind", aad_group_id)
                return UNCHANGED
        except ValueError:
            LOG.warning("Teams team %s has an unreadable %s %r", aad_group_id, BOUND_AT, bound_at)
    now = utc_now()
    row.status = RELEASED
    row.updated_at = now
    _write_config_values(session, row.id, {RELEASED_AT: now.isoformat()})
    session.flush()
    _drop_markers_if_unused(session, workspace)
    session.commit()
    _forget_installs({workspace})
    return RELEASED_TEAM


def disconnect_workspace(session, workspace) -> int:
    """Unbind every team of a workspace, delete their channel mappings and its
    marker. Returns the number of teams released."""
    workspace = uuid.UUID(str(workspace))
    _lock(session, {workspace})
    teams = (
        session.query(Integration)
        .filter(Integration.type == TEAM_TYPE, Integration.tenant_id == workspace, Integration.status == ENABLED)
        .all()
    )
    now = utc_now()
    for row in teams:
        row.status = RELEASED
        row.updated_at = now
        _write_config_values(session, row.id, {RELEASED_AT: now.isoformat()})
    group_ids = [row.name for row in teams]
    if group_ids:
        session.query(ChannelAccountMapping).filter(
            ChannelAccountMapping.tenant_id == workspace,
            ChannelAccountMapping.platform == MARKER_TYPE,
            ChannelAccountMapping.team_id.in_(group_ids),
        ).delete(synchronize_session=False)
    _drop_markers(session, workspace)
    session.commit()
    _forget_installs({workspace})
    return len(teams)
