import json
import logging
from json import JSONDecodeError

from fastapi import APIRouter, BackgroundTasks, HTTPException, Request, Response
from fastapi.concurrency import run_in_threadpool
from fastapi.responses import PlainTextResponse

from notifications_server import sync_engine, teams_app, slack_app
from notifications_server.security.webhook_auth import internal_token_valid, slack_signature_valid
from notifications_server.services.actions_common import (
    SlackInteractiveActionsService,
    SlackEventsService,
    MsTeamsEventsService,
    GoogleChatEventsService,
)

LOG = logging.getLogger(__name__)

UNAUTHORIZED = "Unauthorized webhook request"

router = APIRouter(
    prefix="/webhooks",
    tags=["actions"],
    responses={404: {"description": "Not found"}},
)


@router.post("/slack/events")
async def handle_slack_events(request: Request):
    # Direct ingress path: accept the Slack HMAC; edge-forwarded path: the token.
    raw = await request.body()
    if not (internal_token_valid(request.headers) or slack_signature_valid(raw, request.headers)):
        raise HTTPException(status_code=401, detail=UNAUTHORIZED)

    try:
        payload = json.loads(raw or b"{}")
    except JSONDecodeError:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    if not isinstance(payload, dict):
        raise HTTPException(status_code=400, detail="Invalid data format")

    # Echo the URL-verification challenge before dispatch (the service expects a
    # real event payload).
    if payload.get("type") == "url_verification":
        return PlainTextResponse(payload.get("challenge", ""))

    service = SlackEventsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)
    try:
        await run_in_threadpool(service.execute_event, payload)
    finally:
        service.close()


@router.post("/slack/interactive")
async def handle_slack_interactive_action(request: Request):
    # Interactive arrives only via the edge (token); the HMAC branch is kept for
    # symmetry with /slack/events.
    raw = await request.body()
    if not (internal_token_valid(request.headers) or slack_signature_valid(raw, request.headers)):
        raise HTTPException(status_code=401, detail=UNAUTHORIZED)

    try:
        payload = json.loads(raw or b"{}")
    except JSONDecodeError:
        raise HTTPException(status_code=400, detail="Invalid JSON")
    actions = payload.get("actions") if isinstance(payload, dict) else None
    if not isinstance(actions, list) or len(actions) == 0:
        msg = f"Illegal trigger request {payload}"
        raise HTTPException(status_code=400, detail={"error": msg, "actions": actions})

    service = SlackInteractiveActionsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)
    try:
        await run_in_threadpool(service.execute_action, payload)
    finally:
        service.close()


@router.api_route("/msteams/events", methods=["POST", "OPTIONS"])
async def handle_ms_teams_events(request: Request, background_tasks: BackgroundTasks):
    if request.method == "OPTIONS":
        return Response(status_code=200)

    # The edge verifies the Bot Framework JWT; gate this hop on the internal token.
    if not internal_token_valid(request.headers):
        raise HTTPException(status_code=401, detail=UNAUTHORIZED)

    try:
        payload = await request.json()
    except JSONDecodeError as e:
        LOG.warning(f"Teams event did not contain valid JSON. {e}")
        raise HTTPException(status_code=400, detail="Invalid JSON")

    service = MsTeamsEventsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)

    async def execute_and_close():
        try:
            await service.execute_event(payload)
        finally:
            service.close()

    background_tasks.add_task(execute_and_close)

    return Response(status_code=200, content="ok")


@router.api_route("/google-chat/events", methods=["POST"])
async def handle_google_chat_events(request: Request, background_tasks: BackgroundTasks):
    # The edge verifies the Google JWT; gate this hop on the internal token.
    if not internal_token_valid(request.headers):
        raise HTTPException(status_code=401, detail=UNAUTHORIZED)

    try:
        payload = await request.json()
    except JSONDecodeError as e:
        LOG.warning(f"Google Chat event did not contain valid JSON. {e}")
        raise HTTPException(status_code=400, detail="Invalid JSON")

    service = GoogleChatEventsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)

    async def execute_and_close():
        try:
            await service.execute_event(payload)
        finally:
            service.close()

    background_tasks.add_task(execute_and_close)

    # Return a JSON ack rather than plain text. Google Chat treats the response
    # body as the bot's synchronous reply; a non-JSON body ("ok") on a
    # CARD_CLICKED event surfaces as "unable to process your request" in the
    # client. The empty-object response says "received, handling asynchronously"
    # — the actual reply is posted via the Chat API in the background task.
    return {}
