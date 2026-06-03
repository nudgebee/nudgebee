import json
import logging
import threading

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

# The only scope a bot needs to receive events, post messages (with cards),
# and update its own messages. Does not require admin approval.
CHAT_BOT_SCOPE = "https://www.googleapis.com/auth/chat.bot"


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
            credentials = service_account.Credentials.from_service_account_info(sa_info, scopes=[CHAT_BOT_SCOPE])
            cls._service = build("chat", "v1", credentials=credentials, cache_discovery=False)
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
