"""Discord renderer for Auto Optimize scheduled-execution notifications.

Mirrors message_templates/google_chat/auto_optimize_scheduled_notification.py:
schedule summary plus the task list (one embed field per task). The Slack
skip/delay callbacks are interactive-only and have no Discord equivalent.
Returns the {"content", "embeds"} payload consumed by DiscordClient.chat_post.
"""

from typing import Any, Dict

from notifications_server.configs.settings import settings
from notifications_server.message_templates.discord.embed_utils import _v, clamp_embeds
from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import (
    AutopilotScheduledParams,
)

AUTO_OPTIMIZE_COLOR = 3447003  # blue (#3498db)


def get_discord_auto_optimize_schedule_message_template(auto_pilot_pr: AutopilotScheduledParams) -> Dict[str, Any]:
    details_url = settings.urls.auto_pilot_task_url(
        auto_pilot_id=auto_pilot_pr.auto_pilot_id,
        utm_source="discord",
    )
    description = (
        f"**Auto Optimize** '{auto_pilot_pr.autopilot_name}' has been scheduled for"
        f" **{auto_pilot_pr.execution_on}**.\n\n"
        "**Following tasks will be executed by auto optimize:**\n\n"
        f"Visit the autopilot console for more info: [Auto Pilot]({details_url})"
    )
    fields = [
        {
            "name": f"Task {idx}: {_v(task.task_type)}",
            "value": (
                f"**Resource Name:** {_v(task.resource_details.name)}\n"
                f"**Resource Namespace:** {_v(task.resource_details.namespace)}\n"
                f"**Resource Type:** {_v(task.resource_details.type)}"
            ),
            "inline": False,
        }
        for idx, task in enumerate(auto_pilot_pr.tasks, start=1)
    ]
    embed = {
        "title": f"Auto Optimize Scheduled: {auto_pilot_pr.autopilot_name}",
        "description": description,
        "color": AUTO_OPTIMIZE_COLOR,
        "fields": fields,
        "footer": {"text": "Author: Autopilot"},
    }
    content = f"Auto Optimize '{auto_pilot_pr.autopilot_name}' scheduled for {auto_pilot_pr.execution_on}"
    return {"content": content, "embeds": clamp_embeds([embed])}
