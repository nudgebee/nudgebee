import html
import json
import logging
import uuid as uuid_module
from datetime import timedelta
from logging import Logger
from typing import Optional, Dict, Callable, Sequence

from slack_bolt.error import BoltError
from slack_bolt.oauth import OAuthFlow
from slack_bolt.oauth.callback_options import (
    FailureArgs,
    SuccessArgs,
    DefaultCallbackOptions,
)

from slack_bolt.oauth.oauth_settings import OAuthSettings
from slack_bolt.request import BoltRequest
from slack_bolt.response import BoltResponse
from slack_sdk.errors import SlackApiError
from slack_sdk.oauth import OAuthStateUtils
from slack_sdk.web import WebClient, SlackResponse

from slack_bolt.util.utils import create_web_client

from sqlalchemy.orm import Session

from notifications_server.configs.settings import public_ip, settings
from notifications_server.message_templates.base import render_success_page
from notifications_server.models.enums import EventCategory, EventType, EventActor, EventAction, EventStatus
from notifications_server.models.models import MessagingPlatform
from notifications_server.repositories.oauth_repository import (
    find_installation_by_tenant_and_platform,
    get_state_tenant,
    update_state_tenant,
)
from notifications_server.services.audit import create_audit_request
from notifications_server.utils.datetime_utils import utc_now
from notifications_server.utils.user_util import get_user_id_by_email

CHARSET_UTF_ = "text/html; charset=utf-8"


def get_installation_aborted_message(description):
    branding_name = settings.urls.branding_name
    base_url = html.escape(public_ip())
    return (
        '<html> <head> <link rel="icon" href="data:,"> <style> body { padding: 10px 15px;'
        " font-family: verdana; text-align: center; } </style> </head> <body>"
        ' <h2 align="center">Slack Installation Aborted</h2>'
        f' <p align="center">Slack installation aborted due to, {description},'
        f' to go back to {branding_name} click <a href="{base_url}">here</a></p>'
        " </body> </html>"
    )


def get_tenant_id_by_state(state):
    from notifications_server import sync_engine

    with Session(sync_engine) as session:
        return get_state_tenant(session, state)


class SlackOAuthFlow(OAuthFlow):
    settings: OAuthSettings
    client_id: str
    redirect_uri: Optional[str]
    install_path: str
    redirect_uri_path: str

    success_handler: Callable[[SuccessArgs], BoltResponse]
    failure_handler: Callable[[FailureArgs], BoltResponse]

    @property
    def client(self) -> WebClient:
        if self._client is None:
            self._client = create_web_client(logger=self.logger)
        return self._client

    @property
    def logger(self) -> Logger:
        if self._logger is None:
            self._logger = logging.getLogger(__name__)
        return self._logger

    def __init__(self, *, settings: OAuthSettings, client: Optional[WebClient] = None, logger: Optional[Logger] = None):
        """The module to run the Slack app installation flow (OAuth flow).

        Args:
            client: The `slack_sdk.web.WebClient` instance.
            logger: The logger.
            settings: OAuth settings to configure this module.
        """
        super().__init__(settings=settings, client=client, logger=logger)
        self._state_utils = OAuthStateUtils(
            cookie_name="slack-app-oauth-state",
            expiration_seconds=60 * 10,
        )
        self._client = client
        self._logger = logger
        self.settings = settings
        self.settings.logger = self._logger

        self.client_id = self.settings.client_id
        self.redirect_uri = self.settings.redirect_uri
        self.install_path = self.settings.install_path
        self.redirect_uri_path = self.settings.redirect_uri_path

        self.default_callback_options = DefaultCallbackOptions(
            logger=logger,
            state_utils=self.settings.state_utils,
            redirect_uri_page_renderer=self.settings.redirect_uri_page_renderer,
        )
        if settings.callback_options is None:
            settings.callback_options = self.default_callback_options
        self.success_handler = settings.callback_options.success
        self.failure_handler = settings.callback_options.failure

    # -----------------------------
    # Installation
    # -----------------------------

    def handle_installation(self, request: BoltRequest) -> BoltResponse:
        set_cookie_value: Optional[str] = None
        url = self.build_authorize_url("", request)
        tenant = request.query.get("tenant", [None])[0]
        if tenant is None:
            return BoltResponse(
                status=401,
                body="",
                headers={},
            )
        try:
            uuid_module.UUID(tenant)
        except (ValueError, AttributeError):
            return BoltResponse(
                status=400,
                body=json.dumps({"error": "Invalid tenant_id format"}),
                headers={},
            )

        from notifications_server import sync_engine

        with Session(sync_engine) as session:
            installations = find_installation_by_tenant_and_platform(session, tenant, "slack")
        if len(installations) > 0:
            json_error = json.dumps({"error": "Installation already exists for tenant"})
            return BoltResponse(
                status=403,
                body=json_error,
                headers=self.append_set_cookie_headers(
                    {},
                    set_cookie_value,
                ),
            )
        if self.settings.state_validation_enabled:
            state = self.issue_new_state(request)
            url = self.build_authorize_url(state, request)
            set_cookie_value = self.settings.state_utils.build_set_cookie_for_new_state(state)
            from notifications_server import sync_engine

            with Session(sync_engine) as session:
                update_state_tenant(session, state, tenant)
        if self.settings.install_page_rendering_enabled:
            json_url = json.dumps({"url": url})
            return BoltResponse(
                status=200,
                body=json_url,
                headers=self.append_set_cookie_headers(
                    {},
                    set_cookie_value,
                ),
            )
        else:
            return BoltResponse(
                status=302,
                body="",
                headers=self.append_set_cookie_headers(
                    {"Content-Type": CHARSET_UTF_, "Location": url},
                    set_cookie_value,
                ),
            )

    # ----------------------
    # Internal methods for Installation
    # ----------------------

    def issue_new_state(self, request: BoltRequest) -> str:
        return self.settings.state_store.issue()

    def build_authorize_url(self, state: str, request: BoltRequest) -> str:
        team_ids: Optional[Sequence[str]] = request.query.get("team")
        return self.settings.authorize_url_generator.generate(
            state=state,
            team=team_ids[0] if team_ids is not None else None,
        )

    def append_set_cookie_headers(self, headers: dict, set_cookie_value: Optional[str]):
        if set_cookie_value is not None:
            headers["Set-Cookie"] = [set_cookie_value]
        return headers

    # -----------------------------
    # Callback
    # -----------------------------

    def handle_callback(self, request: BoltRequest) -> BoltResponse:
        # failure due to end-user's cancellation or invalid redirection to slack.com
        error = request.query.get("error", [None])[0]
        if error is not None:
            return self.handle_callback_error(error, request)

        state = request.query.get("state", [None])[0]
        user_email = request.headers.get("x-user-email", [None])[0]
        tenant = get_tenant_id_by_state(state)
        # state parameter verification
        if self.settings.state_validation_enabled:
            valid_state_consumed = self.settings.state_store.consume(state)
            if not valid_state_consumed:
                return self.failure_handler(
                    FailureArgs(
                        request=request,
                        reason="invalid_state",
                        suggested_status_code=401,
                        settings=self.settings,
                        default=self.default_callback_options,
                    )
                )
        # run installation
        code = request.query.get("code", [None])[0]
        if code is None:
            return self.failure_handler(
                FailureArgs(
                    request=request,
                    reason="missing_code",
                    suggested_status_code=401,
                    settings=self.settings,
                    default=self.default_callback_options,
                )
            )

        if not tenant:
            self.logger.error("No tenant_id found for OAuth state: %s", state)
            return BoltResponse(
                status=400,
                headers={"Content-Type": CHARSET_UTF_},
                body=get_installation_aborted_message("missing tenant association for this OAuth state"),
            )

        installation = self.run_installation(code)
        if installation is None:
            # failed to run installation with the code
            return self.failure_handler(
                FailureArgs(
                    request=request,
                    reason="invalid_code",
                    suggested_status_code=401,
                    settings=self.settings,
                    default=self.default_callback_options,
                )
            )
        installation.tenant_id = tenant
        # persist the installation
        try:
            self.store_installation(request, installation)
            user_id = get_user_id_by_email(user_email)
            create_audit_request(
                user_id=user_id,
                tenant_id=tenant,
                account_id=None,
                event_time=utc_now(),
                event_category=EventCategory.NOTIFICATIONS.value,
                event_type=EventType.NOTIFICATION_SLACK_CONFIGURATION_CREATE.value,
                event_prev_state=None,
                event_state={"status": "success"},
                event_actor=EventActor.SLACK.value,
                event_target="notification_configuration",
                event_action=EventAction.CREATE.value,
                event_status=EventStatus.SUCCESS.value,
                event_attr={"status": "success"},
            )
        except BoltError as err:
            return self.failure_handler(
                FailureArgs(
                    request=request,
                    reason="storage_error",
                    error=err,
                    suggested_status_code=500,
                    settings=self.settings,
                    default=self.default_callback_options,
                )
            )
        # display a successful completion page to the end-user
        return BoltResponse(
            status=200,
            headers={
                "Content-Type": "text/html; charset=utf-8",
                "Set-Cookie": self._state_utils.build_set_cookie_for_deletion(),
            },
            body=render_success_page("slack", public_ip()),
        )

    def handle_callback_error(self, error, request):
        if error == "access_denied":
            return BoltResponse(
                status=302,  # 302 Found status code indicates temporary redirection
                headers={"Location": public_ip()},
            )
        else:
            return self.failure_handler(
                FailureArgs(
                    request=request,
                    reason=error,
                    suggested_status_code=200,
                    settings=self.settings,
                    default=self.default_callback_options,
                )
            )

    # ----------------------
    # Internal methods for Callback
    # ----------------------

    def run_installation(self, code: str) -> Optional[MessagingPlatform]:
        try:
            oauth_response: SlackResponse = self.client.oauth_v2_access(
                code=code,
                client_id=self.settings.client_id,
                client_secret=self.settings.client_secret,
                redirect_uri=self.settings.redirect_uri,  # can be None
            )
            installed_team: Dict[str, str] = oauth_response.get("team") or {}
            installer: Dict[str, str] = oauth_response.get("authed_user") or {}

            bot_token: Optional[str] = oauth_response.get("access_token")
            # NOTE: oauth.v2.access doesn't include bot_id in response
            bot_id: Optional[str] = None
            if bot_token is not None:
                auth_test = self.client.auth_test(token=bot_token)
                bot_id = auth_test["bot_id"]

            expires_in = oauth_response.get("expires_in")
            token_expires_at = utc_now() + timedelta(seconds=expires_in) if expires_in else None

            return MessagingPlatform(
                id=uuid_module.uuid4(),
                username=installer.get("id"),
                app_id=oauth_response.get("app_id"),
                team_id=installed_team.get("id"),
                team_name=installed_team.get("name"),
                token=bot_token,
                token_expires_at=token_expires_at,
                scopes=oauth_response.get("scope"),  # comma-separated string
                bot_id=bot_id,
            )

        except SlackApiError as e:
            message = f"Failed to fetch oauth.v2.access result with code: {code} - error: {e}"
            self.logger.warning(message)
            return None

    def store_installation(self, request: BoltRequest, installation: MessagingPlatform):
        # may raise BoltError
        self.settings.installation_store.save(installation)
