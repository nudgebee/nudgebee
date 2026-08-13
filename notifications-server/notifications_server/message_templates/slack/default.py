import json

from pydantic import BaseModel
from typing import Dict, Any, Optional

from notifications_server.configs.settings import settings
from notifications_server.message_templates.slack.recommendation_nudge_digest import (
    STRIPE_NEUTRAL,
    header_block,
    legacy_attachment,
)


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

    lines = []
    if params.message:
        lines.append(params.message)
    if params.parameters:
        lines.append(format_parameters(params.parameters))

    message = {"blocks": [header_block(title)], "unfurl_links": False}
    if lines:
        message["attachments"] = [legacy_attachment(STRIPE_NEUTRAL, title, text="\n".join(lines))]
    return message
