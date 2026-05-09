import asyncio
import json
import logging
from typing import Optional, Dict, Any
from fastapi import APIRouter, BackgroundTasks, HTTPException, Request
from pydantic import BaseModel, ValidationError
from sqlalchemy.orm import Session

from notifications_server import sync_engine, slack_app, teams_app
from notifications_server.services.common import CommonService
from notifications_server.services.events import Events
from notifications_server.services.cache import Cache
from notifications_server.models.models import MessagingPlatform
from notifications_server.utils.feature_flag_utils import is_feature_enabled

LOG = logging.getLogger(__name__)

INVALID_CONVERSATION_ID_MSG = "Invalid conversation id or notification not found"

router = APIRouter(
    prefix="/llm",
    tags=["llm_callbacks"],
    responses={404: {"description": "Not found"}},
)

event_cache = Cache()


class LLMResponse(BaseModel):
    conversation_id: str
    type: str  # "follow-up" or "final"
    response: str
    tenant_id: Optional[str] = None
    session_id: Optional[str] = None


async def get_cached_or_fallback_entry(
    common_service: CommonService,
    payload: LLMResponse,
) -> tuple[Dict[str, Any], str, str, Optional[str], Optional[str]]:
    conversation_id = payload.conversation_id

    channel_id, thread_ts, team_id, account_id = _parse_conversation(common_service, conversation_id, payload)

    cached_entry = await asyncio.to_thread(event_cache.get_event_entry, thread_ts)

    if not cached_entry and not team_id and payload.tenant_id:
        team_id = await asyncio.to_thread(_lookup_team_id, payload.tenant_id)

    if not cached_entry and team_id:
        cached_entry = {
            "channel_id": channel_id,
            "team_id": team_id,
            "account_id": account_id,
            "session_id": conversation_id,
            "is_thread": False,
        }
        event_cache.cache_event_entry(thread_ts, cached_entry)

    if not cached_entry:
        LOG.warning(
            "No cached entry found: conversation_id=%s, thread_ts=%s, tenant_id=%s",
            payload.conversation_id,
            thread_ts,
            payload.tenant_id,
        )
        raise HTTPException(status_code=404, detail="Session not found in cache")

    _validate_platform(cached_entry, thread_ts)

    return (
        cached_entry,
        channel_id,
        thread_ts,
        cached_entry.get("team_id"),
        cached_entry.get("account_id"),
    )


def _parse_conversation(common_service: CommonService, conversation_id: str, payload: LLMResponse):
    if conversation_id.startswith("event-"):
        return _handle_event_conversation(common_service, conversation_id, payload)

    # Google Chat thread_names look like "spaces/SPACE_ID/threads/THREAD_ID"
    if conversation_id.startswith("spaces/"):
        return None, conversation_id, None, None

    if conversation_id.startswith(("a:", "19:")):
        thread_ts = conversation_id.split("-_-")[0]
        return None, thread_ts, None, None

    parts = conversation_id.split("-")
    if len(parts) != 2:
        LOG.warning(INVALID_CONVERSATION_ID_MSG, extra={"conversation_id": conversation_id})
        raise HTTPException(status_code=404, detail=INVALID_CONVERSATION_ID_MSG)

    channel_id, thread_ts = parts
    return channel_id, thread_ts, None, None


def _handle_event_conversation(common_service: CommonService, conversation_id: str, payload: LLMResponse):
    channel_id, thread_ts, team_id, account_id = common_service.get_channel_and_ts_from_sent_notifications(
        conversation_id
    )

    if payload.tenant_id:
        with Session(sync_engine) as session:
            if is_feature_enabled(session, "EVENT_ANALYSIS_ON_CHANNEL", payload.tenant_id):
                parts = conversation_id.split("-", 1)
                if len(parts) == 2 and parts[1]:
                    incident_uuid = parts[1]
                    common_service.get_incident_details_and_reply_on_channel(incident_uuid, payload)
                else:
                    LOG.warning(
                        "Could not extract incident_uuid from conversation_id for incident reply",
                        extra={"conversation_id": conversation_id},
                    )

    if not (channel_id and thread_ts):
        LOG.debug(INVALID_CONVERSATION_ID_MSG, extra={"conversation_id": conversation_id})
        raise HTTPException(status_code=404, detail=INVALID_CONVERSATION_ID_MSG)

    return channel_id, thread_ts, team_id, account_id


def _lookup_team_id(tenant_id: str) -> Optional[str]:
    try:
        with Session(sync_engine) as session:
            platform = session.query(MessagingPlatform).filter_by(tenant_id=tenant_id, platform="slack").first()
            if platform:
                return platform.team_id
    except Exception as e:
        LOG.warning("Failed to look up team_id for tenant %s: %s", tenant_id, e)
    return None


def _validate_platform(cached_entry: dict, thread_ts: str):
    platform = cached_entry.get("platform", "slack")
    if platform == "slack" and not cached_entry.get("team_id"):
        LOG.warning("No team_id found for Slack platform", extra={"thread_ts": thread_ts})
        raise HTTPException(status_code=404, detail="Session not found in cache")
    # Google Chat and MS Teams don't require team_id


@router.post("/response")
async def handle_llm_response(request: Request, background_tasks: BackgroundTasks):
    body = await request.body()
    try:
        data = json.loads(body)
    except (json.JSONDecodeError, UnicodeDecodeError) as e:
        LOG.error("Invalid JSON body on POST /llm/response: %s", e)
        raise HTTPException(status_code=400, detail="Invalid JSON body")
    try:
        payload = LLMResponse(**data)
    except ValidationError as e:
        LOG.error("Validation error on POST /llm/response: %s", e)
        raise HTTPException(status_code=422, detail=str(e))
    LOG.debug(
        "Received LLM response: conversation_id=%s, type=%s, tenant_id=%s, session_id=%s, response_length=%d",
        payload.conversation_id,
        payload.type,
        payload.tenant_id,
        payload.session_id,
        len(payload.response) if payload.response else 0,
    )

    with CommonService(engine=sync_engine, slack_app=slack_app, teams_app=teams_app) as common_service:
        events_service = Events(common_service, slack_app, teams_app, None)
        try:
            cached_entry, channel_id, thread_ts, team_id, _ = await get_cached_or_fallback_entry(
                common_service, payload
            )
            platform = cached_entry.get("platform", "slack")

            LOG.debug(
                "Routing LLM response: conversation_id=%s, type=%s, platform=%s, channel_id=%s",
                payload.conversation_id,
                payload.type,
                platform,
                channel_id,
            )

            handlers = {
                "slack": {
                    "follow-up": events_service.handle_followup_response,
                    "final": events_service.handle_final_response,
                },
                "ms_teams": {
                    "follow-up": events_service.handle_teams_followup_response,
                    "final": events_service.handle_teams_final_response,
                },
                "google_chat": {
                    "follow-up": events_service.handle_gchat_followup_response,
                    "final": events_service.handle_gchat_final_response,
                },
            }

            platform_handlers = handlers.get(platform)
            if not platform_handlers:
                LOG.error("Unknown platform '%s' for conversation_id=%s", platform, payload.conversation_id)
                raise HTTPException(status_code=400, detail=f"Unknown platform: {platform}")

            handler = platform_handlers.get(payload.type)
            if not handler:
                LOG.error("Unknown response type '%s' for conversation_id=%s", payload.type, payload.conversation_id)
                raise HTTPException(status_code=400, detail=f"Unknown response type: {payload.type}")

            if platform == "ms_teams":
                background_tasks.add_task(handler, payload, cached_entry, thread_ts)
            elif platform == "google_chat":
                background_tasks.add_task(handler, payload, cached_entry, thread_ts)
            else:
                handler(payload, cached_entry, channel_id, thread_ts, team_id)

            return {"status": "success"}

        except HTTPException:
            raise
        except Exception as e:
            LOG.error(
                "Error processing LLM response: conversation_id=%s, type=%s, error=%s",
                payload.conversation_id,
                payload.type,
                e,
                exc_info=True,
            )
            raise HTTPException(status_code=500, detail="Internal server error")
