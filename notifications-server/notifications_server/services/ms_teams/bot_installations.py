"""Keep the recorded Teams teams (ms_teams_team integrations) current from the
bot's activities: a team's name and service URL are refreshed while it is bound,
and the app leaving a team releases it. Binding is team_binding's alone, so
nothing here creates, re-enables or moves a row. Activities reaching here were
already verified at the edge, so their tenant and team ids are trusted."""

import asyncio
import logging
import time
from typing import Any, Dict, List, Tuple

from botbuilder.schema import Activity
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from notifications_server.models.models import Integration, IntegrationConfigValue
from notifications_server.services.messaging_installations import _decrypt_config, _write_config_values
from notifications_server.services.ms_teams.activity import TEAM_TYPE, channel_data_of
from notifications_server.services.ms_teams.team_binding import (
    ENABLED,
    RELEASED_TEAM,
    TEAM_CONFIG_KEYS,
    release_team,
)
from notifications_server.utils.datetime_utils import utc_now

LOG = logging.getLogger(__name__)

INSTALL_ACTIONS = {"add", "add-upgrade"}
UNINSTALL_ACTIONS = {"remove", "remove-upgrade"}
TEAM_UPSERT_EVENTS = {"teamRenamed", "teamRestored", "teamUnarchived"}
TEAM_REMOVE_EVENTS = {"teamDeleted", "teamHardDeleted", "teamArchived"}

RECORDED, REMOVED, UNCHANGED, NOT_CONNECTED = "recorded", "removed", "unchanged", "not_connected"

# Teams already recorded from a message in this process, so the next messages
# from the same team skip the database. Install and uninstall events always write.
SEEN_TTL_SECONDS = 600
_seen_from_messages: Dict[str, Tuple[Tuple, float]] = {}


def _bot_in(members, activity: Activity) -> bool:
    recipient_id = activity.recipient.id if activity.recipient else None
    return bool(recipient_id) and any(m.id == recipient_id for m in members or [])


def _actions(activity: Activity) -> List[str]:
    channel_data = channel_data_of(activity)
    if activity.type == "installationUpdate":
        if activity.action in INSTALL_ACTIONS:
            return ["upsert"]
        if activity.action in UNINSTALL_ACTIONS:
            return ["remove"]
        return []
    if activity.type == "conversationUpdate":
        event_type = channel_data.get("eventType")
        actions = []
        if _bot_in(activity.members_added, activity) or event_type in TEAM_UPSERT_EVENTS:
            actions.append("upsert")
        if _bot_in(activity.members_removed, activity) or event_type in TEAM_REMOVE_EVENTS:
            actions.append("remove")
        return actions
    if activity.type == "message":
        # Teams leaves some team fields out of install events; messages refresh them.
        return ["upsert"]
    return []


def team_changes(activity: Activity) -> List[Dict[str, Any]]:
    """The ms_teams_team changes an activity implies. Only team-scope activities
    count; personal and group chats are not recorded here."""
    channel_data = channel_data_of(activity)
    team = channel_data.get("team") or {}
    aad_tenant_id = (channel_data.get("tenant") or {}).get("id")
    if not (team.get("aadGroupId") and aad_tenant_id):
        return []
    recipient_id = (activity.recipient.id if activity.recipient else "") or ""
    fields = {
        "team_aad_group_id": team["aadGroupId"],
        "team_thread_id": team.get("id") or "",
        "team_name": team.get("name") or "",
        "service_url": activity.service_url or "",
        "aad_tenant_id": aad_tenant_id,
        # Teams addresses a bot as "28:<app id>".
        "app_id": recipient_id.split(":", 1)[-1],
    }
    from_message = activity.type == "message"
    return [
        {"action": action, "from_message": from_message, "event_time": getattr(activity, "timestamp", None), **fields}
        for action in _actions(activity)
    ]


def apply_team_change(session, change: Dict[str, Any]) -> str:
    """Refresh a bound team's stored fields, or release the team when the app
    left it. An empty field keeps the stored value, and an unchanged team
    writes nothing."""
    if change["action"] == "remove":
        released = release_team(session, change["team_aad_group_id"], change.get("event_time"))
        return REMOVED if released == RELEASED_TEAM else UNCHANGED
    integ = (
        session.query(Integration)
        .filter(Integration.type == TEAM_TYPE, Integration.name == change["team_aad_group_id"])
        .first()
    )
    if integ is None or integ.status != ENABLED:
        return NOT_CONNECTED
    config = {key: (change.get(key) or None) for key in TEAM_CONFIG_KEYS}
    rows = session.query(IntegrationConfigValue).filter(IntegrationConfigValue.integration_id == integ.id).all()
    stored = _decrypt_config(rows)
    if all(value is None or stored.get(key) == value for key, value in config.items()):
        return UNCHANGED
    integ.updated_at = utc_now()
    _write_config_values(session, integ.id, config)
    session.commit()
    return RECORDED


def store_team_name(session, integration_id, name: str) -> None:
    """Save the name of a team that was recorded without one."""
    _write_config_values(session, integration_id, {"team_name": name})
    session.commit()


def _signature(change: Dict[str, Any]) -> Tuple:
    return tuple(change.get(key) for key in TEAM_CONFIG_KEYS)


def _seen_recently(change: Dict[str, Any]) -> bool:
    entry = _seen_from_messages.get(change["team_aad_group_id"])
    return bool(entry) and entry[0] == _signature(change) and entry[1] > time.monotonic()


async def record_team_installations(activity: Activity, engine) -> None:
    """Apply an activity's team changes off the event loop."""
    changes = [c for c in team_changes(activity) if not (c["from_message"] and _seen_recently(c))]
    if not changes:
        return

    def _apply() -> List[str]:
        results = []
        with Session(engine) as session:
            for change in changes:
                try:
                    results.append(apply_team_change(session, change))
                except IntegrityError as e:
                    session.rollback()
                    LOG.warning("Teams team %s not recorded: %s", change["team_aad_group_id"], e.orig)
                    results.append(None)
        return results

    for change, result in zip(changes, await asyncio.to_thread(_apply)):
        group_id = change["team_aad_group_id"]
        if change["from_message"] and result in (RECORDED, UNCHANGED):
            _seen_from_messages[group_id] = (_signature(change), time.monotonic() + SEEN_TTL_SECONDS)
        elif not change["from_message"]:
            _seen_from_messages.pop(group_id, None)
        if result == NOT_CONNECTED:
            LOG.info("Teams team %s has the bot but isn't connected to a workspace", group_id)
        elif result in (RECORDED, REMOVED):
            LOG.info("Teams team %s %s", group_id, result)
