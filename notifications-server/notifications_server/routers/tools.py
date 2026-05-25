import logging
from typing import Any, Dict
from uuid import UUID, uuid4

from fastapi import APIRouter, HTTPException, Request
from fastapi.concurrency import run_in_threadpool
from fastapi.responses import HTMLResponse, RedirectResponse, JSONResponse
from slack_bolt.request import BoltRequest

from sqlalchemy.orm import Session

from notifications_server import teams_app, slack_app, sync_engine
from notifications_server.configs.settings import settings
from notifications_server.exceptions.exceptions import Err
from notifications_server.message_templates.base import render_success_page
from notifications_server.repositories.oauth_repository import (
    find_installation_by_tenant_and_platform,
    update_state_tenant,
)
from notifications_server.services.google_chat.google_chat import GoogleChatService
from notifications_server.services.ms_teams.ms_teams import MsTeamsService

OAUTH_NOT_FOUND = "OAuth flow not configured"

INSTALLATION_ALREADY_EXISTS = "Installation already exists for tenant"

LOG = logging.getLogger(__name__)

router = APIRouter(
    prefix="/api/integrations",
    tags=["tools"],
    responses={404: {"description": "Not found"}},
)


@router.get("/install/ms-teams")
async def install_teams():
    auth_url = teams_app.get_authorization_request_url(
        scopes=settings.ms_teams.scopes, state=str(uuid4()), redirect_uri=settings.ms_teams_redirect_uri
    )
    return RedirectResponse(url=auth_url)


@router.post("/callback/ms-teams")
async def ms_teams_oauth_redirect(request: Request, data: Dict[Any, Any]):
    code = data["code"]
    tenant_id = data["tenant_id"]

    LOG.info(f"MS Teams Installation request for tenant: {tenant_id}")
    with Session(sync_engine) as session:
        installations = find_installation_by_tenant_and_platform(session, tenant_id, "ms_teams")
    if len(installations) > 0:
        LOG.info(f"MS Teams Installation exists for tenant: {tenant_id}")
        raise HTTPException(status_code=400, detail=INSTALLATION_ALREADY_EXISTS)

    user_email = request.headers.get("x-user-email", None)

    token_result = teams_app.acquire_token_by_authorization_code(
        code, scopes=settings.ms_teams.scopes, redirect_uri=settings.ms_teams_redirect_uri
    )
    account = teams_app.get_accounts()[0]
    service = MsTeamsService(sync_engine)
    service.save_teams_installation(teams_app.client_id, tenant_id, token_result, account, user_email)
    success_html = render_success_page("MS-Teams", settings.base_url)

    return HTMLResponse(content=success_html)


_google_oauth_code_verifiers: Dict[str, str] = {}


@router.get("/install/google")
async def google_chat_install():
    service = GoogleChatService(sync_engine)
    flow = service.get_google_flow()
    flow.redirect_uri = settings.google_chat_redirect_uri
    authorization_url, state = flow.authorization_url(
        access_type="offline", prompt="consent", include_granted_scopes="true", state=str(uuid4())
    )
    if flow.code_verifier:
        _google_oauth_code_verifiers[state] = flow.code_verifier
    return {"url": authorization_url}


@router.post("/callback/google")
async def google_chat_oauth_redirect(request: Request, data: Dict[Any, Any]):
    tenant_id = data.pop("tenant_id", None)
    if tenant_id is None:
        raise ValueError(400, Err.OS0010, ["tenant_id"])

    user_email = request.headers.get("x-user-email", None)

    LOG.info(f"Google Chat Installation request for tenant: {tenant_id}")
    with Session(sync_engine) as session:
        installations = find_installation_by_tenant_and_platform(session, tenant_id, "google_chat")
    if len(installations) > 0:
        LOG.info(f"Google Chat Installation exists for tenant: {tenant_id}")
        raise HTTPException(status_code=400, detail=INSTALLATION_ALREADY_EXISTS)

    state = request.query_params.get("state")
    code_verifier = _google_oauth_code_verifiers.pop(state, None) if state else None

    service = GoogleChatService(sync_engine)
    flow = service.get_google_flow()
    flow.redirect_uri = settings.google_chat_redirect_uri
    full_uri = "https://" + request.url.hostname + request.url.path + "?" + request.url.query
    flow.fetch_token(authorization_response=full_uri, code_verifier=code_verifier)
    credentials = flow.credentials
    service.save_google_chat_installation(tenant_id, credentials, user_email)
    success_html = render_success_page("Google Chat", settings.base_url)

    return HTMLResponse(content=success_html)


slack_router = APIRouter(
    prefix="/slack",
    tags=["tools"],
    responses={404: {"description": "Not found"}},
)


class SlackBase:
    def __init__(self, slack_app):
        self.slack_app = slack_app

    @staticmethod
    async def bolt_request(request: Request) -> BoltRequest:
        body = await request.body() if request.body else b""
        body_str = body.decode("utf-8") if body else ""
        query_params = dict(request.query_params)
        headers = dict(request.headers)
        return BoltRequest(
            body=body_str,
            query=query_params,
            headers=headers,
        )


# Use Dependency Injection to pass the Slack App
def get_slack_base() -> SlackBase:
    return SlackBase(slack_app=slack_app)


@slack_router.get("/oauth_redirect")
async def slack_oauth_handler(
    request: Request,
):
    slack_base = get_slack_base()
    if slack_base.slack_app.oauth_flow is not None:
        oauth_flow = slack_base.slack_app.oauth_flow
        bolt_request = await slack_base.bolt_request(request)
        LOG.info("Slack Installation request for tenant")
        _ = await run_in_threadpool(oauth_flow.handle_callback, bolt_request)
        success_html = render_success_page("Slack", settings.base_url)
        return HTMLResponse(content=success_html)
    else:
        raise HTTPException(status_code=404, detail=OAUTH_NOT_FOUND)


@slack_router.get("/install")
async def slack_installation_handler(request: Request):
    slack_base = get_slack_base()
    if slack_base.slack_app.oauth_flow is not None:
        oauth_flow = slack_base.slack_app.oauth_flow
        oauth_flow.settings.user_scopes = ["admin.users:read"]
        bolt_request = await slack_base.bolt_request(request)
        tenant_id = request.headers.get("tenant-id")

        if not tenant_id:
            raise HTTPException(status_code=403, detail="Tenant id missing for installation")
        try:
            UUID(tenant_id)
        except (ValueError, AttributeError):
            raise HTTPException(status_code=400, detail="Invalid tenant_id format")

        with Session(sync_engine) as session:
            installations = find_installation_by_tenant_and_platform(session, tenant_id, "slack")
        if len(installations) > 0:
            raise HTTPException(status_code=403, detail=INSTALLATION_ALREADY_EXISTS)
        else:
            state = oauth_flow.issue_new_state(bolt_request)
            with Session(sync_engine) as session:
                update_state_tenant(session, state, tenant_id)
            url = oauth_flow.build_authorize_url(state, bolt_request)
            set_cookie_value = oauth_flow.settings.state_utils.build_set_cookie_for_new_state(state)
            response = {"url": url}
            return JSONResponse(content=response, headers={"set-cookie": set_cookie_value})
    else:
        raise HTTPException(status_code=404, detail=OAUTH_NOT_FOUND)
