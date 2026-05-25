__all__ = [
    "get_ms_teams_auto_pilot_scheduled_message_template",
]

from typing import List, Dict, Any

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import (
    AutopilotScheduledParams,
    TaskConfig,
)
import logging

LOG = logging.getLogger(__name__)

TEMPLATE_DEFAULT_DATE_FORMAT = "%a %d %B %H:%M UTC"


def get_ms_teams_auto_pilot_scheduled_message_template(auto_pilot_pr: AutopilotScheduledParams) -> Dict[str, Any]:
    def create_tasks_section(tasks: List[TaskConfig]) -> List[Dict[str, Any]]:
        task_items = []
        for task in tasks:
            task_items.append(
                {
                    "type": "FactSet",
                    "facts": [
                        {"title": "Task Type:", "value": task.task_type},
                        {"title": "Resource Name:", "value": task.resource_details.name or "N/A"},
                        {"title": "Resource Namespace:", "value": task.resource_details.namespace or "N/A"},
                        {"title": "Resource Type:", "value": task.resource_details.type or "N/A"},
                    ],
                }
            )
        return task_items

    details_url = settings.urls.auto_pilot_task_url(
        auto_pilot_id=str(auto_pilot_pr.auto_pilot_id),
        utm_source=URLRoutes.UTMSource.TEAMS,
    )

    card = {
        "$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
        "type": "AdaptiveCard",
        "version": "1.4",
        "msteams": {"width": "full"},
        "body": [
            {
                "type": "ColumnSet",
                "columns": [
                    {
                        "type": "Column",
                        "items": [
                            {
                                "type": "TextBlock",
                                "weight": "Bolder",
                                "text": settings.urls.branding_name,
                                "wrap": True,
                            },
                        ],
                        "width": "stretch",
                    },
                ],
            },
            {
                "type": "TextBlock",
                "text": (
                    f"Auto Optimize '{auto_pilot_pr.autopilot_name}' has been scheduled for"
                    f" {auto_pilot_pr.execution_on.strftime(TEMPLATE_DEFAULT_DATE_FORMAT)}.\n\n"
                    "**Following tasks will be executed by auto optimize:**"
                ),
                "wrap": True,
            },
            *create_tasks_section(auto_pilot_pr.tasks),
            {
                "type": "TextBlock",
                "text": f"[View in Auto Pilot Console]({details_url})",
                "wrap": True,
            },
        ],
        "actions": [
            {
                "type": "Action.OpenUrl",
                "title": "Open Auto Pilot",
                "url": details_url,
            }
        ],
    }

    return card
