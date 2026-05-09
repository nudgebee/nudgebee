import logging
from json import JSONDecodeError
from typing import Any, Dict

from fastapi import APIRouter, BackgroundTasks, HTTPException, Request, Response

from notifications_server import sync_engine, teams_app, slack_app
from notifications_server.services.actions_common import (
    SlackInteractiveActionsService,
    SlackEventsService,
    MsTeamsEventsService,
    GoogleChatEventsService,
)

LOG = logging.getLogger(__name__)

router = APIRouter(
    prefix="/webhooks",
    tags=["actions"],
    responses={404: {"description": "Not found"}},
)


@router.post("/slack/events")
def handle_slack_events(payload: Dict[Any, Any]):
    if not isinstance(payload, dict):
        raise HTTPException(status_code=400, detail="Invalid data format")

    service = SlackEventsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)
    try:
        service.execute_event(payload)
    finally:
        service.close()


@router.post("/slack/interactive")
def handle_slack_interactive_action(payload: Dict[Any, Any]):
    if len(payload.get("actions", [])) == 0:
        msg = f"Illegal trigger request {payload}"
        raise HTTPException(status_code=400, detail={"error": msg, "actions": payload.get("actions")})
    service = SlackInteractiveActionsService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app)
    try:
        service.execute_action(payload)
    finally:
        service.close()


@router.api_route("/msteams/events", methods=["POST", "OPTIONS"])
async def handle_ms_teams_events(request: Request, background_tasks: BackgroundTasks):
    if request.method == "OPTIONS":
        return Response(status_code=200)

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

    return Response(status_code=200, content="ok")
