from typing import List

from notifications_server.configs.settings import settings, URLRoutes
from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import (
    AutopilotScheduledParams,
    TaskConfig,
)
import logging

LOG = logging.getLogger(__name__)


def get_google_chat_auto_optimize_schedule_message_template(auto_pilot_pr: AutopilotScheduledParams) -> str:
    def create_tasks_markdown(tasks: List[TaskConfig]) -> str:
        tasks_markdown = ""
        for task in tasks:
            tasks_markdown += (
                f"*Task Type*: {task.task_type}"
                f"\n*Resource Name*: {task.resource_details.name}"
                f"\n*Resource Namespace*: {task.resource_details.namespace}\n\n"
            )
        return tasks_markdown

    details_url = settings.urls.auto_pilot_task_url(
        auto_pilot_id=auto_pilot_pr.auto_pilot_id,
        utm_source=URLRoutes.UTMSource.GCHAT,
    )
    message = (
        f"{settings.urls.branding_link('gchat')}\n\nAuto Optimize has *{auto_pilot_pr.status}* on"
        f" {auto_pilot_pr.execution_on}.\n\n*Following tasks will be executed by auto"
        f" optimize:*\ntask:*\n{create_tasks_markdown(auto_pilot_pr.tasks)}\n"
        f"<{details_url}|Auto Pilot>\u200b"
    )

    return message
