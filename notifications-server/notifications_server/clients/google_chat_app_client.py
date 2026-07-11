import json
import logging
import threading

import google_auth_httplib2
import googleapiclient.http
import httplib2
from google.oauth2 import service_account
from googleapiclient.discovery import build
from googleapiclient.errors import HttpError

from notifications_server.clients.google_chat_client import (
    _handle_retryable_error,
    _normalize_space,
    is_retryable_error,
    parse_http_error,
)
from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)

# Baseline scope: receive events, post messages (with cards), update own messages.
# Does not require admin approval.
CHAT_BOT_SCOPE = "https://www.googleapis.com/auth/chat.bot"

# Lets the app manage its own space memberships (self-join / leave). The Workspace
# admin must authorize this scope for the app in Google before membership calls
# succeed; until then Google returns 403, which we surface for the guided-grant UI.
CHAT_MEMBERSHIPS_SCOPE = "https://www.googleapis.com/auth/chat.app.memberships"

# Lets the app create named spaces (find-or-create destinations for runbooks). Like
# the memberships scope, the Workspace admin must authorize it before spaces.create
# succeeds; until then Google returns 403, surfaced as reason='needs_authorization'.
CHAT_SPACES_CREATE_SCOPE = "https://www.googleapis.com/auth/chat.spaces.create"


class GoogleChatAppClient:
    """Service-account-authenticated Chat API client used for bot reply paths.

    The legacy `GoogleChatClient` authenticates with the tenant installer's
    user-OAuth token, which Google rejects for any message containing cards.
    This client authenticates as the Chat app itself, which is the only
    credential type that may post cards, update messages, or respond as a bot.

    Used today by `gchat_reply_in_thread` and `gchat_reply_with_card` when
    `GOOGLE_CHAT_SA_KEY` is set. Outbound notification-rule code paths still
    use the user-OAuth client.
    """

    RATE_LIMIT_RETRIES = settings.google_chat.rate_limit_retries
    RATE_LIMIT_SLEEP = settings.google_chat.rate_limit_sleep

    _service = None
    _service_lock = threading.Lock()

    @classmethod
    def is_enabled(cls) -> bool:
        return settings.google_chat.is_app_auth_enabled

    @classmethod
    def _get_service(cls):
        if cls._service is not None:
            return cls._service
        with cls._service_lock:
            if cls._service is not None:
                return cls._service
            sa_key = settings.google_chat.sa_key
            if not sa_key:
                raise ValueError("Google Chat service account key is not configured (GOOGLE_CHAT_SA_KEY).")
            sa_info = json.loads(sa_key)
            credentials = service_account.Credentials.from_service_account_info(
                sa_info, scopes=[CHAT_BOT_SCOPE, CHAT_MEMBERSHIPS_SCOPE, CHAT_SPACES_CREATE_SCOPE]
            )

            # Each transport binds a hard socket timeout: googleapiclient's default
            # httplib2.Http() has timeout=None, so a stalled TLS session would hang the
            # calling thread until the OS TCP timeout (minutes) — see GOOGLE_CHAT_API_TIMEOUT.
            def _authed_http():
                return google_auth_httplib2.AuthorizedHttp(
                    credentials, http=httplib2.Http(timeout=settings.google_chat.api_timeout)
                )

            # This service is a cross-thread singleton (outbound sends run via
            # asyncio.to_thread; the inbound event handler now runs in threadpool workers).
            # httplib2.Http is NOT thread-safe — a shared instance corrupts its connection
            # pool under concurrency (SSLError / IncompleteRead). requestBuilder hands every
            # API request a fresh AuthorizedHttp so the service can be shared safely.
            def _build_request(_http, *args, **kwargs):
                return googleapiclient.http.HttpRequest(_authed_http(), *args, **kwargs)

            cls._service = build(
                "chat", "v1", http=_authed_http(), requestBuilder=_build_request, cache_discovery=False
            )
            return cls._service

    @classmethod
    def post_message(cls, space, message, tenant=None, thread_name=None):
        """Post a text or card message into a space as the Chat app.

        Mirrors the return shape of `GoogleChatClient.post_to_google_chat` so
        the two clients are drop-in interchangeable at call sites.
        """
        # Copy dict inputs — we add a "thread" key below and must not mutate
        # callers' message templates (which are often module-level constants).
        message_body = message.copy() if isinstance(message, dict) else {"text": message}
        space_id = _normalize_space(space)

        create_kwargs = {"parent": space_id, "body": message_body}
        if thread_name:
            message_body.setdefault("thread", {"name": thread_name})
            create_kwargs["messageReplyOption"] = "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"

        max_retries = cls.RATE_LIMIT_RETRIES
        retry_sleep = cls.RATE_LIMIT_SLEEP
        last_error_message = None

        for attempt in range(max_retries + 1):
            try:
                service = cls._get_service()
                response = service.spaces().messages().create(**create_kwargs).execute()
                LOG.debug("Google Chat app message response: %s", response)
                return {
                    "success": True,
                    "message_ts": response.get("name"),
                    "channel_id": space_id,
                    "thread_name": response.get("thread", {}).get("name"),
                    "raw": response,
                }

            except HttpError as e:
                status_code, error_status, error_message = parse_http_error(e)
                last_error_message = error_message

                if not is_retryable_error(e):
                    LOG.error(
                        "Google Chat (app auth) API error for tenant %s: %s (status=%s, code=%d)",
                        tenant,
                        error_message,
                        error_status,
                        status_code,
                    )
                    return {
                        "success": False,
                        "channel_id": space_id,
                        "reason": error_status or "api_error",
                        "error": error_message,
                    }

                if not _handle_retryable_error(e, attempt + 1, max_retries, retry_sleep, tenant):
                    break

            except Exception as e:
                LOG.exception("Unexpected error sending Google Chat app message for tenant %s", tenant)
                return {"success": False, "channel_id": space_id, "reason": "unexpected_error", "error": str(e)}

        return {
            "success": False,
            "channel_id": space_id,
            "reason": "rate_limit_exceeded",
            "error": last_error_message or "Max retries exceeded",
        }

    @classmethod
    def leave_space(cls, space):
        """Remove the Chat app's own membership from a space (the bot leaves).

        Uses the `members/app` alias, which Google Chat resolves to the calling
        app's own membership when authenticated as the service account. Best
        effort: failures are logged and returned, never raised.
        """
        space_id = _normalize_space(space)
        try:
            service = cls._get_service()
            service.spaces().members().delete(name=f"{space_id}/members/app").execute()
            return {"success": True, "channel_id": space_id}
        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat (app auth) leave-space error for %s: %s (status=%s)",
                space_id,
                error_message,
                status_code,
            )
            return {
                "success": False,
                "channel_id": space_id,
                "reason": error_status or "api_error",
                "error": error_message,
            }
        except Exception as e:
            LOG.exception("Unexpected error leaving Google Chat space %s", space_id)
            return {"success": False, "channel_id": space_id, "reason": "unexpected_error", "error": str(e)}

    @classmethod
    def join_space(cls, space):
        """Add the Chat app's own membership to a space (the bot self-joins).

        Requires the chat.app.memberships scope to be authorized for the app in the
        target Workspace; until an admin grants it Google returns 403, surfaced as
        reason='needs_authorization' so the UI can prompt the admin. 409 (already a
        member) is treated as success. Best effort: never raises.
        """
        space_id = _normalize_space(space)
        try:
            service = cls._get_service()
            membership = (
                service.spaces()
                .members()
                .create(parent=space_id, body={"member": {"name": "users/app", "type": "BOT"}})
                .execute()
            )
            return {"success": True, "channel_id": space_id, "raw": membership}
        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            if status_code == 409:
                return {"success": True, "channel_id": space_id, "reason": "already_member"}
            reason = "needs_authorization" if status_code == 403 else (error_status or "api_error")
            LOG.error(
                "Google Chat (app auth) join-space error for %s: %s (status=%s)",
                space_id,
                error_message,
                status_code,
            )
            return {"success": False, "channel_id": space_id, "reason": reason, "error": error_message}
        except Exception as e:
            LOG.exception("Unexpected error joining Google Chat space %s", space_id)
            return {"success": False, "channel_id": space_id, "reason": "unexpected_error", "error": str(e)}

    @classmethod
    def membership_status(cls, space):
        """Best-effort probe of the app's membership/permission in a space, for the
        guided 'grant join-permission' UI. Returns one of:
          already_member | can_join | needs_authorization | error
        needs_authorization (403) means an admin hasn't authorized chat.app.memberships.
        """
        space_id = _normalize_space(space)
        try:
            service = cls._get_service()
            service.spaces().members().get(name=f"{space_id}/members/app").execute()
            return {"status": "already_member", "channel_id": space_id}
        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            if status_code == 404:
                return {"status": "can_join", "channel_id": space_id}
            if status_code == 403:
                return {"status": "needs_authorization", "channel_id": space_id}
            return {"status": "error", "channel_id": space_id, "reason": error_status, "error": error_message}
        except Exception as e:
            LOG.exception("Unexpected error probing Google Chat membership for %s", space_id)
            return {"status": "error", "channel_id": space_id, "error": str(e)}

    @classmethod
    def find_space_by_display_name(cls, display_name, tenant=None):
        """Return the first space (the app is a member of) whose displayName matches.

        Used by the find-or-create path. Google Chat's spaces.list filter does not
        support displayName, so we page through named spaces and match client-side.
        Returns the space resource name (e.g. "spaces/AAA") or None.
        """
        try:
            service = cls._get_service()
            page_token = None
            while True:
                response = (
                    service.spaces().list(filter='spaceType = "SPACE"', pageSize=1000, pageToken=page_token).execute()
                )
                for space in response.get("spaces", []):
                    if space.get("displayName") == display_name:
                        return space.get("name")
                page_token = response.get("nextPageToken")
                if not page_token:
                    return None
        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat (app auth) list-spaces error for tenant %s: %s (status=%s)",
                tenant,
                error_message,
                status_code,
            )
            return None
        except Exception:
            LOG.exception("Unexpected error listing Google Chat spaces for tenant %s", tenant)
            return None

    @classmethod
    def create_space(cls, display_name, tenant=None):
        """Create a named Google Chat space as the Chat app.

        Requires the chat.spaces.create scope to be authorized for the app in the
        target Workspace; until an admin grants it Google returns 403, surfaced as
        reason='needs_authorization' so the UI can prompt the admin.
        """
        try:
            service = cls._get_service()
            space = service.spaces().create(body={"spaceType": "SPACE", "displayName": display_name}).execute()
            return {
                "success": True,
                "channel_id": space.get("name"),
                "name": space.get("displayName", display_name),
                "url": space.get("spaceUri"),
                "raw": space,
            }
        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            reason = "needs_authorization" if status_code == 403 else (error_status or "api_error")
            LOG.error(
                "Google Chat (app auth) create-space error for tenant %s: %s (status=%s)",
                tenant,
                error_message,
                status_code,
            )
            return {"success": False, "reason": reason, "error": error_message}
        except Exception as e:
            LOG.exception("Unexpected error creating Google Chat space for tenant %s", tenant)
            return {"success": False, "reason": "unexpected_error", "error": str(e)}
