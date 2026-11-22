"""Small helpers shared by the Teams bot modules."""

from typing import Any, Dict

TEAM_TYPE = "ms_teams_team"


def channel_data_of(activity) -> Dict[str, Any]:
    """The activity's Teams channelData, or {} when it has none."""
    channel_data = getattr(activity, "channel_data", None)
    return channel_data if isinstance(channel_data, dict) else {}


def team_of(activity) -> Dict[str, Any]:
    """The activity's channelData.team, or {}."""
    team = channel_data_of(activity).get("team")
    return team if isinstance(team, dict) else {}


def in_team(activity) -> bool:
    """Whether the activity is in a team: it carries team data, whatever its
    conversation type says, or it says it's in a channel."""
    if team_of(activity).get("aadGroupId") or team_of(activity).get("id"):
        return True
    return getattr(getattr(activity, "conversation", None), "conversation_type", None) == "channel"


def mentions_bot(activity) -> bool:
    """Whether the activity @mentions the bot it was sent to."""
    recipient_id = getattr(getattr(activity, "recipient", None), "id", None)
    for entity in getattr(activity, "entities", None) or []:
        if isinstance(entity, dict):
            kind, data = entity.get("type"), entity
        else:
            kind, data = getattr(entity, "type", None), getattr(entity, "additional_properties", None) or {}
        mentioned = data.get("mentioned") if isinstance(data, dict) else None
        if kind == "mention" and recipient_id and isinstance(mentioned, dict) and mentioned.get("id") == recipient_id:
            return True
    return False


def in_chat(activity) -> bool:
    """Whether the activity is in a personal or group chat, with no team data."""
    conversation_type = getattr(getattr(activity, "conversation", None), "conversation_type", None)
    return not in_team(activity) and conversation_type in ("personal", "groupChat")
