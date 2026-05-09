import logging
from typing import Dict, Any, List, Optional

from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException, Request
from fastapi.responses import JSONResponse

from notifications_server import engine, sync_engine, slack_app, teams_app
from notifications_server.configs.settings import settings
from notifications_server.schemas.message import (
    SendMessageRequest,
    PlatformResponse,
    GetThreadMessagesRequest,
    GetThreadMessagesResponse,
)
from notifications_server.services.common import CommonService
from notifications_server.services.message import MessageService
from notifications_server.services.rules import NotificationRulesService

LOG = logging.getLogger(__name__)

# Singleton MessageService instance (created lazily on first use)
_message_service: Optional[MessageService] = None


def _get_message_service() -> MessageService:
    global _message_service
    if _message_service is None:
        _message_service = MessageService(engine=engine, slack_app=slack_app, teams_app=teams_app)
    return _message_service


# Error message constants
ERROR_MISSING_TENANT_HEADER = "Missing tenant header"
ERROR_TENANT_NOT_FOUND = "Tenant id not found"

ACTION_TOKEN_HEADER = "X-ACTION-TOKEN"


def verify_action_token(request: Request):
    """Verify that the incoming request has a valid X-ACTION-TOKEN header.
    Matches the auth pattern used by api-server's authHandlerMiddleware."""
    token = request.headers.get(ACTION_TOKEN_HEADER)
    expected = settings.action_api_server_token
    if not expected or token != expected:
        raise HTTPException(status_code=401, detail="Unauthorized")


router = APIRouter(
    prefix="/api",
    tags=["common"],
    responses={404: {"description": "Not found"}},
)


@router.post("/send_message", status_code=201, response_model=List[PlatformResponse])
async def send_message(payload: SendMessageRequest) -> List[PlatformResponse]:
    controller = _get_message_service()

    result = await controller.send_message(payload)

    # Handle both response types:
    # - List[Dict]: Normal platform responses
    # - Dict: Queued/cached messages (SLO, anomaly) - return empty list since not sent yet
    if isinstance(result, dict):
        # For queued/cached messages, return empty list (no platforms sent to yet)
        LOG.debug(f"Message queued/cached with status: {result.get('status')}")
        return []

    # Return list of platform responses
    return result


@router.post("/channels/list", dependencies=[Depends(verify_action_token)])
async def list_channels(request: Request, body: Dict[Any, Any]):
    try:
        session_variables = body.get("session_variables", {})
        tenant = session_variables.get("x-hasura-user-tenant-id") or request.headers.get("tenant")
        if not tenant:
            return JSONResponse({"error": {"message": ERROR_TENANT_NOT_FOUND}})

        platform = body.get("input", {}).get("platform")

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            channels = controller.list_channels(platform, tenant)
            return JSONResponse(content=channels)
    except Exception as e:
        return JSONResponse({"error": {"message": "Unable to list channels", "cause": "".join(map(str, e.args))}})


@router.post("/users/list", dependencies=[Depends(verify_action_token)])
async def list_users(request: Request, body: Dict[Any, Any]):
    try:
        session_variables = body.get("session_variables", {})
        tenant = session_variables.get("x-hasura-user-tenant-id") or request.headers.get("tenant")
        if not tenant:
            return JSONResponse({"error": {"message": ERROR_TENANT_NOT_FOUND}})

        platform = body.get("input", {}).get("platform")

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            users = controller.list_users(platform, tenant)
            return JSONResponse(content=users)
    except Exception as e:
        return JSONResponse({"error": {"message": "Unable to list users", "cause": "".join(map(str, e.args))}})


@router.post("/channels/join", status_code=201)
async def join_channel(request: Request, payload: Dict[Any, Any], background_tasks: BackgroundTasks):
    try:
        # Get tenant from header (set by authenticated callers like runbook-server)
        tenant_id = request.headers.get("tenant")
        if not tenant_id:
            return JSONResponse(
                {"error": {"message": ERROR_MISSING_TENANT_HEADER}},
                status_code=401,
            )

        platform = payload.get("platform")
        account_id = payload.get("account_id")
        channel_id = payload.get("channel_id")
        session_id = payload.get("session_id")
        team_id = payload.get("team_id")
        text = payload.get("text")

        # Validate required fields
        if not platform or not account_id or not channel_id:
            return JSONResponse(
                {"error": {"message": "Missing required fields: platform, account_id, channel_id"}},
                status_code=400,
            )

        # Only support Slack for now
        if platform.lower() not in ["slack"]:
            return JSONResponse(
                {
                    "error": {
                        "message": f"Platform {platform} is not supported yet. Only 'slack' is currently supported."
                    }
                },
                status_code=400,
            )

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = controller.join_channel(
                platform=platform.lower(),
                account_id=account_id,
                tenant_id=tenant_id,
                channel_id=channel_id,
                session_id=session_id,
                team_id=team_id,
                text=text,
            )

            if "error" in result:
                return JSONResponse(result, status_code=400)

            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception(f"Error in join_channel endpoint: {e}")
        return JSONResponse({"error": {"message": "Unable to join channel", "cause": str(e)}}, status_code=500)


@router.post("/channels/message", status_code=201)
async def send_channel_message(request: Request, payload: Dict[Any, Any]):
    try:
        # Get tenant from header (set by authenticated callers like runbook-server)
        tenant_id = request.headers.get("tenant")
        if not tenant_id:
            return JSONResponse(
                {"error": {"message": ERROR_MISSING_TENANT_HEADER}},
                status_code=401,
            )

        platform = payload.get("platform")
        channel_id = payload.get("channel_id")
        session_id = payload.get("session_id")
        text = payload.get("text")

        if not platform or not channel_id or not session_id or not text:
            return JSONResponse(
                {"error": {"message": "Missing required fields: platform, channel_id, session_id, text"}},
                status_code=400,
            )

        if platform.lower() not in ["slack"]:
            return JSONResponse(
                {
                    "error": {
                        "message": f"Platform {platform} is not supported yet. Only 'slack' is currently supported."
                    }
                },
                status_code=400,
            )

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = controller.send_channel_message(
                platform=platform.lower(), tenant_id=tenant_id, channel_id=channel_id, session_id=session_id, text=text
            )

            if "error" in result:
                return JSONResponse(result, status_code=400)

            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception(f"Error in send_channel_message endpoint: {e}")
        return JSONResponse({"error": {"message": "Unable to send message", "cause": str(e)}}, status_code=500)


@router.post("/users/message", status_code=201)
async def send_direct_message(request: Request, payload: Dict[Any, Any]):
    try:
        tenant_id = request.headers.get("tenant")
        if not tenant_id:
            return JSONResponse(
                {"error": {"message": ERROR_MISSING_TENANT_HEADER}},
                status_code=401,
            )

        platform = payload.get("platform")
        user_id = payload.get("user_id")
        text = payload.get("text")
        team_id = payload.get("team_id")

        if not platform or not user_id or not text:
            return JSONResponse(
                {"error": {"message": "Missing required fields: platform, user_id, text"}},
                status_code=400,
            )

        if platform.lower() not in ["slack", "ms_teams", "google_chat"]:
            return JSONResponse(
                {
                    "error": {
                        "message": (
                            f"Platform {platform} is not supported for direct messages. "
                            "Supported platforms: slack, ms_teams, google_chat"
                        )
                    }
                },
                status_code=400,
            )

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = controller.send_direct_message(
                platform=platform.lower(),
                tenant_id=tenant_id,
                user_id=user_id,
                text=text,
                team_id=team_id,
            )

            if "error" in result:
                return JSONResponse(result, status_code=400)

            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception(f"Error in send_direct_message endpoint: {e}")
        return JSONResponse({"error": {"message": "Unable to send direct message", "cause": str(e)}}, status_code=500)


@router.post("/notifications/test", dependencies=[Depends(verify_action_token)])
async def send_test_notification(request: Request, body: Dict[Any, Any]):
    try:
        session_variables = body.get("session_variables", {})
        tenant = session_variables.get("x-hasura-user-tenant-id") or request.headers.get("tenant")
        if not tenant:
            return JSONResponse({"success": False, "error": ERROR_TENANT_NOT_FOUND})

        input_data = body.get("input", {})
        platform = input_data.get("platform")
        channel_id = input_data.get("channel_id")
        team_id = input_data.get("team_id")

        if not platform or not channel_id:
            return JSONResponse({"success": False, "error": "Missing required fields: platform, channel_id"})

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = controller.send_test_notification(
                platform=platform,
                tenant_id=tenant,
                channel_id=channel_id,
                team_id=team_id,
            )
            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception("Error in send_test_notification endpoint: %s", e)
        return JSONResponse({"success": False, "error": f"Unexpected error: {str(e)}"})


@router.post("/rules/save", status_code=201, dependencies=[Depends(verify_action_token)])
async def save_or_update_rule(data: Dict[Any, Any]):
    service = NotificationRulesService(engine)
    response = await service.save_or_update_rule(**data)
    response.pop("status_code", None)
    return JSONResponse(response, status_code=201)


@router.post("/send_email", status_code=201)
async def send_email(payload: Dict[Any, Any]):
    """
    Send an email to one or more recipients.

    Request body:
        - recipients: string or array of email addresses (required)
        - subject: Email subject (required)
        - body: Email body content (required if template not provided)
        - body_format: How to render the body — "text" (default), "markdown", or "html"
        - template: Template name to use instead of body (optional)
        - template_params: Parameters for template rendering (optional)
        - reply_to: Reply-to email address (optional)

    Returns:
        - success: boolean
        - sent_to: list of recipients email was sent to
        - errors: list of errors if any recipients failed
    """
    try:
        recipients = payload.get("recipients")
        subject = payload.get("subject")
        body = payload.get("body")
        body_format = payload.get("body_format")
        template = payload.get("template")
        template_params = payload.get("template_params")
        reply_to = payload.get("reply_to")

        # Validate required fields
        if not recipients:
            return JSONResponse(
                {"success": False, "error": "recipients is required"},
                status_code=400,
            )

        if not subject:
            return JSONResponse(
                {"success": False, "error": "subject is required"},
                status_code=400,
            )

        if not body and not template:
            return JSONResponse(
                {"success": False, "error": "Either body or template is required"},
                status_code=400,
            )

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = await controller.send_email(
                recipients=recipients,
                subject=subject,
                body=body,
                body_format=body_format,
                template=template,
                template_params=template_params,
                reply_to=reply_to,
            )

            if not result.get("success"):
                return JSONResponse(result, status_code=400)

            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception(f"Error in send_email endpoint: {e}")
        return JSONResponse({"success": False, "error": f"Unexpected error: {str(e)}"}, status_code=500)


@router.post("/reactions/add", status_code=201)
async def add_reaction(request: Request, payload: Dict[Any, Any]):
    """
    Add a reaction to a message on any supported platform (Slack, MS Teams, Google Chat).

    Request body:
        - platform: "slack" | "ms_teams" | "google_chat"
        - channel_id: Channel/Space ID
        - message_id: Message timestamp/ID
        - emoji: Emoji (name for Slack e.g. "thumbsup", unicode for Teams/GChat e.g. "👍")
        - team_id: Optional team ID (required for MS Teams)

    Headers:
        - tenant: Tenant identifier (required, set by authenticated callers)

    Returns:
        - success: boolean
        - provider: platform name
        - message_id: message identifier
        - reaction: emoji added
        - error: error message if failed
    """
    try:
        # Get tenant from header (set by authenticated callers like runbook-server)
        tenant_id = request.headers.get("tenant")
        if not tenant_id:
            return JSONResponse(
                {"success": False, "error": ERROR_MISSING_TENANT_HEADER},
                status_code=401,
            )

        platform = payload.get("platform")
        channel_id = payload.get("channel_id")
        message_id = payload.get("message_id")
        emoji = payload.get("emoji")
        team_id = payload.get("team_id")

        # Validate required fields
        if not platform or not channel_id or not message_id or not emoji:
            return JSONResponse(
                {
                    "success": False,
                    "error": "Missing required fields: platform, channel_id, message_id, emoji",
                },
                status_code=400,
            )

        with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
            result = controller.add_reaction(
                platform=platform.lower(),
                tenant_id=tenant_id,
                channel_id=channel_id,
                message_id=message_id,
                emoji=emoji,
                team_id=team_id,
            )

            if not result.get("success"):
                return JSONResponse(result, status_code=400)

            return JSONResponse(content=result)
    except Exception as e:
        LOG.exception(f"Error in add_reaction endpoint: {e}")
        return JSONResponse({"success": False, "error": f"Unexpected error: {str(e)}"}, status_code=500)


@router.post("/threads/messages", status_code=200, response_model=GetThreadMessagesResponse)
async def get_thread_messages(request: Request, payload: GetThreadMessagesRequest) -> GetThreadMessagesResponse:
    # Get tenant from header (set by authenticated callers like runbook-server)
    tenant_id = request.headers.get("tenant")
    if not tenant_id:
        return GetThreadMessagesResponse(
            success=False,
            channel_id=payload.channel_id,
            thread_ts=payload.thread_ts,
            messages=[],
            has_more=False,
            error=ERROR_MISSING_TENANT_HEADER,
        )

    with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as controller:
        result = controller.get_thread_messages(
            tenant_id=tenant_id,
            channel_id=payload.channel_id,
            thread_ts=payload.thread_ts,
            team_id=payload.team_id,
        )

    if not result.get("success"):
        return GetThreadMessagesResponse(
            success=False,
            channel_id=payload.channel_id,
            thread_ts=payload.thread_ts,
            messages=[],
            has_more=False,
            error=result.get("error", "Unknown error"),
        )

    return GetThreadMessagesResponse(
        success=True,
        channel_id=result["channel_id"],
        thread_ts=result["thread_ts"],
        messages=result["messages"],
        has_more=result.get("has_more", False),
    )
