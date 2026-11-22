import logging
from typing import Optional

from pydantic import Field

import requests
from sqlalchemy.orm import Session

from notifications_server.configs import settings
from notifications_server.configs.settings import (
    SKIP_AUTO_OPTIMIZE_ENDPOINT,
)
from notifications_server import sync_engine
from notifications_server.message_templates.base import Emojis
from notifications_server.message_templates.blocks import MarkdownBlock
from notifications_server.message_templates.slack.auto_optimize_scheduled_notification import get_skip_durations
from notifications_server.repositories.user_repository import get_user_tenants, get_user_by_email
from notifications_server.services import workflow_approval
from notifications_server.utils.action_requests import ActionParams
from notifications_server.utils.transformer import Transformer

CLUSTER_NOT_FOUND = "Unable to find cluster, make sure cluster name is correct."

LOG = logging.getLogger(__name__)


class AskAIParams(ActionParams):
    channel_id: str
    team_id: str
    action_name: str
    cluster_id: str
    namespace: str
    event_id: str
    search_term: str
    message_ts: str
    tenant_id: str


class BaseParams(ActionParams):
    channel_id: str
    message_ts: str
    team_id: str
    action_name: str


class SkipAutoOptimizeParams(BaseParams):
    user_email: str
    auto_optimize_id: str
    minutes: Optional[str] = Field(default=None, description="minutes to delay execution by")


class ApprovalParams(BaseParams):
    token: str = Field(description="Approval token for workflow")
    status: str = Field(description="Approval status (approved or rejected)")
    user_email: str = Field(description="Email of the user who clicked")


def _get_message(result, is_success, success_msg, failure_msg):
    emoji = Emojis.ThumbsUp.value if is_success else Emojis.Alert.value
    if "message" in result.json():
        return f"{emoji}" + result.json()["message"]
    elif is_success:
        return f"{emoji} {success_msg}"
    else:
        return f"{emoji} {failure_msg}"


def validate_and_get_user_tenants(user_email):
    with Session(sync_engine) as session:
        user_id, tenant_list = get_user_tenants(session, user_email)
    return user_id, tenant_list


def validate_and_get_user_id(user_email):
    with Session(sync_engine) as session:
        user = get_user_by_email(session, user_email.strip())
    if not user:
        return None
    return user.get("id")


class Actions:
    def __init__(self, common_service, slack_app, teams_app, db_session):
        self.slack_app = slack_app
        self.teams_app = teams_app
        self.session = db_session
        self.common_service = common_service

    def skip_auto_optimize_execution(self, params: SkipAutoOptimizeParams):
        """
        This action is for skipping scheduled execution of Auto Optimize.
        """
        url = settings.services.auto_pilot + SKIP_AUTO_OPTIMIZE_ENDPOINT

        minutes = None
        auto_optimize_id = params.auto_optimize_id.split("_")[0]

        if params.minutes:
            minutes = get_skip_durations().get(params.minutes)

        input_data = {"arg1": {"id": params.auto_optimize_id}}

        if minutes:
            input_data["arg1"]["by_minutes"] = minutes

        session_variables = {"user_id": params.user_email}
        payload = {"session_variables": session_variables, "input": input_data}

        LOG.info("Skipping auto optimize for id: %s", auto_optimize_id)
        result = {}
        try:
            result = requests.post(url, json=payload)
            result.raise_for_status()
            status_message = _get_message(result, True, "Scheduled event skipped successfully", "")
        except requests.RequestException as e:
            LOG.warning(e)
            status_message = _get_message(result, False, "", "Failed to skip scheduled event")

        message = f"{status_message},\nPlease login to your {settings.urls.branding_name} account to see more details"
        output_blocks = Transformer.to_slack(MarkdownBlock(message))

        bot = self.common_service.get_slack_installation(params.team_id)
        self.slack_app.client.reply_in_thread(
            text="skip_auto_optimize",
            channel_id=params.channel_id,
            token=bot.token,
            thread_ts=params.message_ts,
            response_type="in_channel",
            replace_original=False,
            blocks=output_blocks,
        )

    def handle_approval_action(self, params: ApprovalParams):
        """
        This action is for handling approval/rejection of workflow requests.
        """
        if not params.token or not params.status:
            LOG.error("Invalid approval params: token present=%s, status=%s", bool(params.token), params.status)
            status_message = f"{Emojis.Alert.value} Invalid approval request"
            output_blocks = Transformer.to_slack(MarkdownBlock(status_message))
            bot = self.common_service.get_slack_installation(params.team_id)
            self.slack_app.client.reply_in_thread(
                text="approval_action_error",
                channel_id=params.channel_id,
                token=bot.token,
                thread_ts=params.message_ts,
                response_type="in_channel",
                replace_original=False,
                blocks=output_blocks,
            )
            return

        LOG.info("Processing approval action, status: %s", params.status)
        status_message = self._approval_message(
            workflow_approval.decide(params.token, params.status, params.user_email), params.status
        )

        try:
            output_blocks = Transformer.to_slack(MarkdownBlock(status_message))
            bot = self.common_service.get_slack_installation(params.team_id)
            self.slack_app.client.reply_in_thread(
                text="approval_action",
                channel_id=params.channel_id,
                token=bot.token,
                thread_ts=params.message_ts,
                response_type="in_channel",
                replace_original=False,
                blocks=output_blocks,
            )
        except Exception as e:
            LOG.exception("Failed to send approval response to Slack: %s", e)

    @staticmethod
    def _approval_message(decision, status) -> str:
        if decision is workflow_approval.Decision.DECIDED:
            # Format status for display (convert snake_case to Title Case)
            return f"{Emojis.ThumbsUp.value} {status.replace('_', ' ').title()}"
        if decision is workflow_approval.Decision.ANSWERED:
            return f"{Emojis.ThumbsUp.value} This approval has already been processed"
        if decision is workflow_approval.Decision.NOT_FOUND:
            return "Approval request not found or expired"
        if decision is workflow_approval.Decision.INVALID:
            return f"Invalid approval request, please check workflow in {settings.urls.branding_name}."
        if decision is workflow_approval.Decision.NOT_A_USER:
            return workflow_approval.not_a_user_message()
        if decision is workflow_approval.Decision.NO_ACCESS:
            return workflow_approval.no_access_message()
        return "Failed to process approval, please try again."
