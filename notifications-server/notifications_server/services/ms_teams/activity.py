"""Small helpers shared by the Teams bot modules."""

from typing import Any, Dict

TEAM_TYPE = "ms_teams_team"


def channel_data_of(activity) -> Dict[str, Any]:
    """The activity's Teams channelData, or {} when it has none."""
    return activity.channel_data if isinstance(activity.channel_data, dict) else {}
