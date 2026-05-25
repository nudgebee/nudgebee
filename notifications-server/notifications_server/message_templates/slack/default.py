import json

from pydantic import BaseModel
from typing import Dict, Any, Optional, List

from notifications_server.configs.settings import settings


class DefaultParams(BaseModel):
    title: Optional[str] = None
    message: Optional[str] = ""
    parameters: Optional[Dict[str, Any]] = None


def format_parameters(parameters: Dict[str, Any]) -> str:
    return f"```\n{json.dumps(parameters, indent=2)}\n```"


def get_default_message_params(**params) -> DefaultParams:
    return DefaultParams(parameters=params)


def get_slack_default_message_template(params: DefaultParams):
    branding_name = settings.urls.branding_name
    title = params.title or f"{branding_name} Notification"
    blocks: List[Dict[str, Any]] = []
    title_block = {
        "type": "section",
        "text": {
            "type": "mrkdwn",
            "text": f"*{title}*",
        },
    }

    blocks.append(title_block)
    if params.message:
        message_block = {"type": "section", "text": {"type": "mrkdwn", "text": params.message}}
        blocks.append(message_block)

    if params.parameters:
        formatted = format_parameters(params.parameters)
        params_block = {"type": "section", "text": {"type": "mrkdwn", "text": formatted}}
        blocks.append(params_block)

    return {"text": f"{branding_name} Notification", "blocks": blocks, "unfurl_links": False}
