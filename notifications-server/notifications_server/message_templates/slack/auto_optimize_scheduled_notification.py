import json
import logging
from datetime import datetime
from typing import Any, Dict, List, Optional, Union
from uuid import UUID

from notifications_server.configs.settings import public_ip
from pydantic import BaseModel

LOG = logging.getLogger(__name__)


class AutopilotPropertyChanges(BaseModel):
    property_name: str
    current_value: str
    new_value: str


class AutoOptimizeResourceFilter(BaseModel):
    name: str | None = None
    namespace: str | None = None
    type: str | None = None


class TaskConfig(BaseModel):
    action: str
    task_type: str
    description: str
    resource_details: AutoOptimizeResourceFilter


class AutopilotScheduledParams(BaseModel):
    status: str
    execution_on: datetime
    auto_pilot_id: UUID
    autopilot_name: str
    notification_type: str = "auto_pilot_schedule_notification"
    tasks: List[TaskConfig]
    tenant_id: UUID
    cluster: str
    auto_pilot_link: str


def get_auto_pilot_scheduled_message_params(**params) -> AutopilotScheduledParams:
    auto_pilot_pr = AutopilotScheduledParams(**params)
    return auto_pilot_pr


def get_slack_auto_pilot_scheduled_message_template(auto_pilot_pr: AutopilotScheduledParams):
    blocks: List[Dict[str, Any]] = []

    action_block = add_callback(
        "Skip Current execution",
        "skip_auto_optimize_execution",
        auto_pilot_pr,
    )

    title_block = {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": (
                f"*Auto Optimize* '{auto_pilot_pr.autopilot_name}' has been scheduled for"
                f" *<!date^{int(auto_pilot_pr.execution_on.timestamp())}^{{date_short_pretty}} {{time}}|April 14th,"
                " 2024 12:00 PM>*.\n\n *Following tasks will be executed by auto optimize:*"
            ),
        },
    }
    divider_block = {"type": "divider"}
    task_details = []
    for task in auto_pilot_pr.tasks:
        task_detail = {
            "type": "section",
            "fields": [
                {"type": "mrkdwn", "text": f"*Task Type:*\n{task.task_type}"},
                {"type": "mrkdwn", "text": f"*Resource Namespace:*\n{task.resource_details.namespace}"},
                {"type": "mrkdwn", "text": f"*Resource Name:*\n{task.resource_details.name}"},
                {"type": "mrkdwn", "text": f"*Resource Type:*\n{task.resource_details.type}"},
            ],
        }
        task_details.append(task_detail)

    author_block = {"type": "context", "elements": [{"type": "plain_text", "text": "Author: Autopilot", "emoji": True}]}

    def more_info():
        return {
            "type": "section",
            "text": {
                "type": "mrkdwn",
                "text": (
                    "autopilot console for more info by clicking,"
                    f" <{public_ip()}/auto-pilot/task/{auto_pilot_pr.auto_pilot_id}|Auto Pilot>"
                ),
            },
        }

    blocks.extend(
        [
            title_block,
            divider_block,
            *task_details,
            divider_block,
            more_info(),
            divider_block,
            *action_block,
            author_block,
        ]
    )
    return {"text": "Autopilot changes", "blocks": blocks, "unfurl_links": False}


def add_skip_action(action, action_params):
    return {
        "body": {
            "action_name": action,
            "action_params": action_params.model_dump_json(),
            "origin": "callback",
        },
        "no_sinks": False,
    }


def get_skip_durations():
    return {
        "5 Minutes": 5,
        "10 Minutes": 10,
        "15 Minutes": 15,
        "30 Minutes": 30,
        "1 Hour": 60,
        "3 Hours": 180,
        "12 Hours": 720,
        "1 Day": 1440,
        "3 days": 4320,
        "7 Days": 10080,
    }


def add_callback(title, action, action_params: AutopilotScheduledParams):
    return [
        {
            "type": "section",
            "text": {"type": "mrkdwn", "text": " - Select duration you want to delay execution by:"},
            "accessory": {
                "type": "static_select",
                "placeholder": {"type": "plain_text", "text": "Select duration", "emoji": True},
                "options": [
                    {
                        "text": {"type": "plain_text", "text": f"{text}", "emoji": True},
                        "value": f"{action_params.auto_pilot_id}_{text}",
                    }
                    for text, value in get_skip_durations().items()
                ],
                "action_id": "skip_auto_optimize_action_by_minute",
            },
        },
        {
            "type": "section",
            "text": {"type": "mrkdwn", "text": " - Skip current scheduled execution: "},
            "accessory": {
                "type": "button",
                "style": "danger",
                "text": {"type": "plain_text", "text": title},
                "action_id": "trigger_playbook_skip_0",
                "value": json.dumps(add_skip_action(action, action_params)),
            },
        },
    ]
