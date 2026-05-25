import json
import logging
import time
from typing import Optional, Tuple

import requests
from googleapiclient.discovery import build
from googleapiclient.errors import HttpError
from oauth2client.client import AccessTokenCredentials

from notifications_server.configs import Configs
from notifications_server.configs.settings import settings

LOG = logging.getLogger(__name__)

# Constants
USER_AGENT = "my-user-agent/1.0"

# Google API error statuses that should trigger a retry
RETRYABLE_STATUSES = {"RESOURCE_EXHAUSTED", "UNAVAILABLE", "DEADLINE_EXCEEDED"}


def parse_http_error(error: HttpError) -> Tuple[int, Optional[str], str]:
    """
    Parse a Google API HttpError to extract structured error information.

    Args:
        error: The HttpError exception from googleapiclient

    Returns:
        Tuple of (status_code, error_status, error_message)
        - status_code: HTTP status code (e.g., 429, 503)
        - error_status: Google API status string (e.g., "RESOURCE_EXHAUSTED") or None
        - error_message: Human-readable error message
    """
    status_code = error.resp.status if error.resp else 0
    error_status = None
    error_message = str(error)

    try:
        # HttpError.content contains the JSON error response
        if error.content:
            error_data = json.loads(error.content.decode("utf-8"))
            error_info = error_data.get("error", {})

            # Extract status from error details or top-level status
            error_status = error_info.get("status")
            if not error_status:
                # Some errors have status in details array
                details = error_info.get("details", [])
                for detail in details:
                    if "status" in detail:
                        error_status = detail.get("status")
                        break

            # Get a cleaner error message
            error_message = error_info.get("message", error_message)
    except (json.JSONDecodeError, AttributeError, KeyError) as e:
        LOG.debug("Could not parse HttpError content: %s", e)

    return status_code, error_status, error_message


def is_retryable_error(error: HttpError) -> bool:
    """
    Determine if a Google API error should be retried.

    Args:
        error: The HttpError exception

    Returns:
        True if the error is retryable (rate limit, resource exhausted, etc.)
    """
    status_code, error_status, _ = parse_http_error(error)

    # Check for retryable status codes
    if status_code in (429, 503, 504):
        return True

    # Check for retryable error statuses
    if error_status and error_status in RETRYABLE_STATUSES:
        return True

    return False


def _handle_retryable_error(
    error: HttpError, retries: int, max_retries: int, retry_sleep: int, tenant: Optional[str]
) -> bool:
    """
    Handle a retryable HttpError by logging and sleeping if retries remain.

    Args:
        error: The HttpError exception
        retries: Current retry count (already incremented)
        max_retries: Maximum allowed retries
        retry_sleep: Seconds to sleep before retry
        tenant: Tenant identifier for logging

    Returns:
        True if should continue retrying, False if retries exhausted
    """
    status_code, error_status, _ = parse_http_error(error)

    if retries > max_retries:
        return False

    LOG.info(
        "Google Chat rate limited for tenant %s (status=%s). Retry %d/%d after %ds...",
        tenant,
        error_status or status_code,
        retries,
        max_retries,
        retry_sleep,
    )
    time.sleep(retry_sleep)
    return True


def _normalize_space(space):
    if isinstance(space, str):
        # Try parse JSON string if possible
        try:
            parsed = json.loads(space)
            space = parsed
        except json.JSONDecodeError:
            return space  # raw id, not JSON

    if isinstance(space, list):
        space = space[0] if space else None

    if isinstance(space, dict):
        return space.get("id")

    return space


class GoogleChatClient:
    RATE_LIMIT_RETRIES = settings.google_chat.rate_limit_retries
    RATE_LIMIT_SLEEP = settings.google_chat.rate_limit_sleep

    @staticmethod
    def refresh_access_token(refresh_token):
        token_endpoint = "https://oauth2.googleapis.com/token"
        payload = {
            "grant_type": "refresh_token",
            "refresh_token": refresh_token,
            "client_id": Configs.GOOGLE_CLIENT_ID,
            "client_secret": Configs.GOOGLE_CLIENT_SECRET,
        }
        response = requests.post(token_endpoint, data=payload)

        if response.status_code == 200:
            return response.json()

        LOG.warning("Error refreshing access token: %s", response.json())
        return None

    @staticmethod
    def list_spaces(token):
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        try:
            space_list = service.spaces().list().execute()
            spaces = []

            for space in space_list.get("spaces", []):
                if space.get("spaceType") == "SPACE":
                    spaces.append({"id": space["name"], "name": space.get("displayName")})

            return spaces

        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat API error listing spaces: %s (status=%s, code=%d)",
                error_message,
                error_status,
                status_code,
            )
            raise

        except Exception:
            LOG.exception("Unexpected error listing Google Chat spaces")
            raise

    @staticmethod
    def post_to_google_chat(token, space, message, tenant, thread_name=None):
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        message_body = message if isinstance(message, dict) else {"text": message}

        space_id = _normalize_space(space)
        max_retries = GoogleChatClient.RATE_LIMIT_RETRIES
        retry_sleep = GoogleChatClient.RATE_LIMIT_SLEEP
        last_error_message = None

        create_kwargs = {"parent": space_id, "body": message_body}
        if thread_name:
            message_body["thread"] = {"name": thread_name}
            create_kwargs["messageReplyOption"] = "REPLY_MESSAGE_FALLBACK_TO_NEW_THREAD"

        for attempt in range(max_retries + 1):
            try:
                response = service.spaces().messages().create(**create_kwargs).execute()
                LOG.debug("Google Chat message response: %s", response)
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
                        "Google Chat API error for tenant %s: %s (status=%s, code=%d)",
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
                LOG.exception("Unexpected error sending Google Chat message for tenant %s", tenant)
                return {"success": False, "channel_id": space_id, "reason": "unexpected_error", "error": str(e)}

        return {
            "success": False,
            "channel_id": space_id,
            "reason": "rate_limit_exceeded",
            "error": last_error_message or "Max retries exceeded",
        }

    @staticmethod
    def create_reaction(token, space_id, message_id, emoji, tenant=None):
        """
        Add a reaction to a message in Google Chat.

        Args:
            token: Google Chat access token
            space_id: Space/room ID (e.g., "spaces/SPACE_NAME")
            message_id: Message resource name (e.g., "spaces/SPACE_NAME/messages/MESSAGE_NAME")
            emoji: Unicode emoji string (e.g., "👍", "😀")
            tenant: Optional tenant identifier for logging

        Returns:
            Dict with success status and details
        """
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        if not message_id.startswith("spaces/"):
            message_id = f"{space_id}/messages/{message_id}"

        reaction_body = {"emoji": {"unicode": emoji}}
        max_retries = GoogleChatClient.RATE_LIMIT_RETRIES
        retry_sleep = GoogleChatClient.RATE_LIMIT_SLEEP
        last_error_message = None

        for attempt in range(max_retries + 1):
            try:
                response = (
                    service.spaces().messages().reactions().create(parent=message_id, body=reaction_body).execute()
                )
                LOG.debug("Google Chat reaction response: %s", response)
                return {
                    "success": True,
                    "provider": "google_chat",
                    "message_id": message_id,
                    "reaction": emoji,
                    "reaction_name": response.get("name"),
                }

            except HttpError as e:
                status_code, error_status, error_message = parse_http_error(e)
                last_error_message = error_message

                if not is_retryable_error(e):
                    LOG.error(
                        "Google Chat API error for tenant %s: %s (status=%s, code=%d)",
                        tenant,
                        error_message,
                        error_status,
                        status_code,
                    )
                    return {"success": False, "provider": "google_chat", "error": error_status or error_message}

                if not _handle_retryable_error(e, attempt + 1, max_retries, retry_sleep, tenant):
                    break

            except Exception as e:
                LOG.exception("Unexpected error adding Google Chat reaction for tenant %s", tenant)
                return {"success": False, "provider": "google_chat", "error": str(e)}

        return {"success": False, "provider": "google_chat", "error": last_error_message or "rate_limit_exceeded"}

    @staticmethod
    def list_space_members(token, space_id, tenant=None):
        """
        List members of a Google Chat space.

        Args:
            token: Google Chat access token
            space_id: Space ID (e.g., "spaces/SPACE_NAME")
            tenant: Optional tenant identifier for logging

        Returns:
            List of member dictionaries with id, name, email, and display_name
        """
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        members = []
        page_token = None

        try:
            while True:
                request = service.spaces().members().list(parent=space_id, pageToken=page_token)
                response = request.execute()

                for member in response.get("members", []):
                    member_info = member.get("member", {})
                    # Skip bot members
                    if member_info.get("type") == "BOT":
                        continue

                    members.append(
                        {
                            "id": member_info.get("name"),
                            "name": member_info.get("displayName"),
                            "real_name": member_info.get("displayName"),
                            "display_name": member_info.get("displayName"),
                            "email": member_info.get("email"),
                        }
                    )

                page_token = response.get("nextPageToken")
                if not page_token:
                    break

            return members

        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat API error listing members for tenant %s: %s (status=%s, code=%d)",
                tenant,
                error_message,
                error_status,
                status_code,
            )
            raise

    @staticmethod
    def list_dm_spaces(token, tenant=None):
        """
        List direct message spaces (1:1 conversations) that the bot is part of.

        Args:
            token: Google Chat access token
            tenant: Optional tenant identifier for logging

        Returns:
            List of DM spaces with user info
        """
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        dm_spaces = []
        page_token = None

        try:
            while True:
                request = service.spaces().list(pageToken=page_token)
                response = request.execute()

                for space in response.get("spaces", []):
                    # Only include DM spaces
                    if space.get("spaceType") == "DIRECT_MESSAGE":
                        dm_spaces.append(
                            {
                                "id": space.get("name"),
                                "name": space.get("displayName"),
                                "type": "dm",
                            }
                        )

                page_token = response.get("nextPageToken")
                if not page_token:
                    break

            return dm_spaces

        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat API error listing DM spaces for tenant %s: %s (status=%s, code=%d)",
                tenant,
                error_message,
                error_status,
                status_code,
            )
            raise

    @staticmethod
    def _resolve_dm_space_id(token, user_id, tenant):
        """
        Resolve the DM space ID for a user.

        If user_id already looks like a DM space, use it directly.
        Otherwise, set up a new DM space with the user.

        Args:
            token: Google Chat access token
            user_id: User's space ID or user resource name
            tenant: Optional tenant identifier for logging

        Returns:
            Tuple of (space_id, error_result). If error_result is not None, return it immediately.
        """
        if user_id.startswith("spaces/"):
            return user_id, None

        try:
            setup_result = GoogleChatClient.setup_direct_message(token, user_id, tenant)
            if not setup_result.get("success"):
                return None, setup_result
            return setup_result.get("space_id"), None
        except Exception as e:
            LOG.error("Failed to setup DM with user %s: %s", user_id, e)
            return None, {"success": False, "error": f"Failed to setup DM: {str(e)}"}

    @staticmethod
    def send_direct_message(token, user_id, message, tenant=None):
        """
        Send a direct message to a user in Google Chat.

        Note: This requires the user to have an existing DM space with the bot,
        or use setup_direct_message to create one first.

        Args:
            token: Google Chat access token
            user_id: User's space ID (e.g., "users/USER_ID") or DM space ID
            message: Message text to send
            tenant: Optional tenant identifier for logging

        Returns:
            Dict with success status and message details
        """
        space_id, error_result = GoogleChatClient._resolve_dm_space_id(token, user_id, tenant)
        if error_result:
            return error_result

        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)
        message_body = {"text": message}
        max_retries = GoogleChatClient.RATE_LIMIT_RETRIES
        retry_sleep = GoogleChatClient.RATE_LIMIT_SLEEP

        for attempt in range(max_retries + 1):
            try:
                response = service.spaces().messages().create(parent=space_id, body=message_body).execute()
                LOG.info("Successfully sent DM to space %s", space_id)
                return {
                    "success": True,
                    "data": {
                        "user_id": user_id,
                        "space_id": space_id,
                        "message_id": response.get("name"),
                        "platform": "google_chat",
                    },
                }

            except HttpError as e:
                if not is_retryable_error(e):
                    status_code, error_status, error_message = parse_http_error(e)
                    LOG.error(
                        "Google Chat API error sending DM for tenant %s: %s (status=%s, code=%d)",
                        tenant,
                        error_message,
                        error_status,
                        status_code,
                    )
                    return {"success": False, "error": error_message}

                if not _handle_retryable_error(e, attempt + 1, max_retries, retry_sleep, tenant):
                    break

            except Exception as e:
                LOG.exception("Unexpected error sending Google Chat DM for tenant %s", tenant)
                return {"success": False, "error": str(e)}

        return {"success": False, "error": "Rate limit exceeded"}

    @staticmethod
    def setup_direct_message(token, user_id, tenant=None):
        """
        Set up a direct message space with a user.

        Args:
            token: Google Chat access token
            user_id: User's resource name (e.g., "users/USER_ID")
            tenant: Optional tenant identifier for logging

        Returns:
            Dict with success status and space_id
        """
        credentials = AccessTokenCredentials(token, USER_AGENT)
        service = build("chat", "v1", credentials=credentials)

        try:
            # Create or get DM space using spaces.setup
            request_body = {
                "space": {"spaceType": "DIRECT_MESSAGE"},
                "memberships": [{"member": {"name": user_id, "type": "HUMAN"}}],
            }

            response = service.spaces().setup(body=request_body).execute()
            space_id = response.get("name")
            LOG.info("DM space setup with user %s: %s", user_id, space_id)

            return {"success": True, "space_id": space_id}

        except HttpError as e:
            status_code, error_status, error_message = parse_http_error(e)
            LOG.error(
                "Google Chat API error setting up DM for tenant %s: %s (status=%s, code=%d)",
                tenant,
                error_message,
                error_status,
                status_code,
            )
            return {"success": False, "error": error_message}
