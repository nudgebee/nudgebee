import asyncio
import json
import logging
from typing import Optional, Dict, Any
from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException, Request
from pydantic import BaseModel, ValidationError
from sqlalchemy.orm import Session

from notifications_server import sync_engine, slack_app, teams_app
from notifications_server.routers.common import verify_action_token
from notifications_server.services.common import CommonService, extract_event_fingerprint
from notifications_server.services.events import Events
from notifications_server.services.cache import Cache
from notifications_server.models.models import MessagingPlatform
from notifications_server.utils.feature_flag_utils import is_feature_enabled
from notifications_server.services.bot_messages import is_conversation_in_progress_error

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
    type: str  # "follow-up", "final", "error", or "watch_registered"
    response: str
    tenant_id: Optional[str] = None
    session_id: Optional[str] = None
    # Per-question correlator (see events.py:build_llm_payload) echoed back
    # unchanged by llm-server. conversation_id/session_id are deliberately
    # reused across every @mention in a bound incident channel, so they can't
    # identify which physical Slack thread this specific response answers —
    # reply_ref can. Optional: absent on requests sent before this field
    # existed, and on responses to non-Slack-chat requests that never set it
    # (e.g. the automated event-investigation pipeline).
    reply_ref: Optional[str] = None
    # Same mechanism as reply_ref, for who asked instead of which thread -- a
    # later message in the thread overwrites the cache's slack_user_id before
    # this response lands (#42744).
    asker_ref: Optional[str] = None
    # From the llm-server watch feature: keys the in-progress status message on
    # "watch_registered", and on "final" lets the handler retire it.
    watch_id: Optional[str] = None


async def get_cached_or_fallback_entry(
    common_service: CommonService,
    payload: LLMResponse,
) -> tuple[Dict[str, Any], str, str, Optional[str], Optional[str]]:
    conversation_id = payload.conversation_id

    channel_id, thread_ts, team_id, account_id = await asyncio.to_thread(
        _parse_conversation, common_service, conversation_id, payload
    )

    cached_entry = await event_cache.aio.get_event_entry(thread_ts)

    if not cached_entry and not team_id and payload.tenant_id:
        team_id = await asyncio.to_thread(_lookup_team_id, payload.tenant_id)

    if not cached_entry and team_id:
        cached_entry = {
            "channel_id": channel_id,
            "team_id": team_id,
            "account_id": account_id,
            "tenant_id": payload.tenant_id,
            "session_id": conversation_id,
            "is_thread": False,
        }
        await event_cache.aio.cache_event_entry(thread_ts, cached_entry)

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
    # A bound incident channel reuses this same conversation_id for every future
    # @mention, so it alone can't tell which physical Slack thread a response
    # belongs to. reply_ref (per-question, set only for a real @mention) wins
    # when present; sent_notifications wins for finding-alert channels; the
    # incident cache below covers channels bound only via slack.join_channel,
    # which sent_notifications never sees.
    channel_id, thread_ts, team_id, account_id = common_service.get_channel_and_ts_from_sent_notifications(
        conversation_id, tenant_id=payload.tenant_id
    )

    reply_ref_channel_id = reply_ref_thread_ts = None
    if payload.reply_ref:
        parts = payload.reply_ref.rsplit("-", 1)
        if len(parts) == 2 and parts[0] and parts[1]:
            reply_ref_channel_id, reply_ref_thread_ts = parts
        else:
            LOG.warning(
                "Malformed reply_ref on event conversation",
                extra={"conversation_id": conversation_id, "reply_ref": payload.reply_ref},
            )

    # Exclude watch_registered only: it carries an empty response, so this path
    # would just log a spurious "Missing required fields". Other types keep
    # their prior behaviour — an allow-list here would silently drop `error`.
    if reply_ref_channel_id and reply_ref_thread_ts:
        channel_id, thread_ts = reply_ref_channel_id, reply_ref_thread_ts
    elif payload.tenant_id and payload.type != "watch_registered":
        with Session(sync_engine) as session:
            if is_feature_enabled(session, "EVENT_ANALYSIS_ON_CHANNEL", payload.tenant_id):
                incident_uuid = extract_event_fingerprint(conversation_id)
                if incident_uuid:
                    # Only overrides on success, so a sent_notifications hit above
                    # (a normal finding-alert channel) isn't wiped out by a miss here.
                    incident_channel_id, incident_thread_ts, incident_team_id, incident_account_id = (
                        common_service.resolve_incident_channel_and_thread(incident_uuid)
                    )
                    if incident_channel_id and incident_thread_ts:
                        # account_id isn't in resolve_incident_channel_and_thread's own
                        # required-fields check, so a cache entry missing it must not
                        # clobber a valid account_id already found via sent_notifications.
                        channel_id, thread_ts, team_id, account_id = (
                            incident_channel_id,
                            incident_thread_ts,
                            incident_team_id,
                            incident_account_id or account_id,
                        )
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


@router.post("/response", dependencies=[Depends(verify_action_token)])
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
            # This request is an llm-server webhook, not a Slack event, so there's
            # no api_app_id to read directly. Restore the one captured when the
            # conversation started so get_slack_installation resolves the same
            # bot that owns this thread, instead of falling back to priority
            # order and possibly picking a different Slack app that shares the
            # same team_id (e.g. two dev bots installed in one workspace).
            common_service.app_id = cached_entry.get("app_id")

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
                    "error": events_service.handle_error_response,
                    "watch_registered": events_service.handle_watch_registered,
                },
                "ms_teams": {
                    "follow-up": events_service.handle_teams_followup_response,
                    "final": events_service.handle_teams_final_response,
                    "error": events_service.handle_teams_error_response,
                },
                "google_chat": {
                    "follow-up": events_service.handle_gchat_followup_response,
                    "final": events_service.handle_gchat_final_response,
                    "error": events_service.handle_gchat_error_response,
                },
            }

            if payload.type == "error":
                # The upstream error text stays server-side; the thread only ever
                # sees the generic copy the error handlers post.
                if is_conversation_in_progress_error(payload.response):
                    LOG.warning(
                        "LLM run rejected (conversation in progress): conversation_id=%s, platform=%s, error=%s",
                        payload.conversation_id,
                        platform,
                        payload.response,
                    )
                else:
                    LOG.error(
                        "LLM run failed: conversation_id=%s, platform=%s, error=%s",
                        payload.conversation_id,
                        platform,
                        payload.response,
                    )

            platform_handlers = handlers.get(platform)
            if not platform_handlers:
                LOG.error("Unknown platform '%s' for conversation_id=%s", platform, payload.conversation_id)
                raise HTTPException(status_code=400, detail=f"Unknown platform: {platform}")

            handler = platform_handlers.get(payload.type)
            if not handler:
                # watch_registered = best-effort Slack-only indicator; other
                # platforms skip rather than 400 (llm-server fires it for any chat).
                if payload.type == "watch_registered":
                    LOG.debug(
                        "watch_registered not supported for platform=%s; skipping (conversation_id=%s)",
                        platform,
                        payload.conversation_id,
                    )
                    return {"status": "skipped"}
                LOG.error("Unknown response type '%s' for conversation_id=%s", payload.type, payload.conversation_id)
                raise HTTPException(status_code=400, detail=f"Unknown response type: {payload.type}")

            if platform == "ms_teams":
                background_tasks.add_task(handler, payload, cached_entry, thread_ts)
            elif platform == "google_chat":
                background_tasks.add_task(handler, payload, cached_entry, thread_ts)
            else:
                # The Slack handlers are sync: Slack API, database and cache calls.
                await asyncio.to_thread(handler, payload, cached_entry, channel_id, thread_ts, team_id)

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
