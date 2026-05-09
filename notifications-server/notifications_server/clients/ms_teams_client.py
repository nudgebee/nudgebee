"""
Microsoft Teams client for sending notifications.

Provides methods for interacting with the MS Graph API to:
- List joined teams and channels
- Post messages to channels
- Reply to message threads
"""

import json
import logging
import re
import time
import uuid
from typing import Optional, List, Dict, Any

import requests

from notifications_server.configs.settings import settings
from notifications_server.exceptions import (
    TeamsError,
    RateLimitedError,
    ChannelNotFoundError,
    DeliveryFailedError,
)

LOG = logging.getLogger(__name__)
GRAPH_API_BASE_URL = "https://graph.microsoft.com"

# Pattern for valid MS Graph API resource IDs (GUIDs, channel IDs, message IDs)
# Valid characters: alphanumeric, hyphens, underscores, colons, periods, and @
# This prevents path traversal attacks using ../ or other special characters
MS_GRAPH_ID_PATTERN = re.compile(r"^[a-zA-Z0-9\-_:@.]+$")


def validate_graph_api_id(value: str, field_name: str) -> str:
    """
    Validate that a Graph API resource ID is safe to use in URL paths.

    Args:
        value: The ID value to validate
        field_name: Name of the field for error messages

    Returns:
        The validated ID value

    Raises:
        ValueError: If the ID contains invalid characters (potential path traversal)
    """
    if not value:
        raise ValueError(f"{field_name} is required")

    if not MS_GRAPH_ID_PATTERN.match(value):
        raise ValueError(f"Invalid {field_name}: contains disallowed characters")

    # Additional check for path traversal sequences
    if ".." in value or "//" in value:
        raise ValueError(f"Invalid {field_name}: potential path traversal detected")

    return value


TEAMS_API_URL = GRAPH_API_BASE_URL + "/v1.0/me/joinedTeams"
CONTENT_TYPE_JSON = "application/json"


class TeamsApiError(TeamsError):
    """Error from MS Teams Graph API."""

    def __init__(
        self,
        message: str,
        tenant_id: Optional[str] = None,
        status_code: Optional[int] = None,
        response_body: Optional[str] = None,
    ):
        super().__init__(message, tenant_id=tenant_id, teams_error_code=str(status_code) if status_code else None)
        self.status_code = status_code
        self.response_body = response_body


def create_teams_message(body: Dict[str, Any]) -> Dict[str, Any]:
    """Create an MS Teams adaptive card message."""
    message = {
        "body": {"contentType": "html", "content": ""},
        "attachments": [],
    }
    attachment = {
        "contentType": "application/vnd.microsoft.card.adaptive",
        "content": json.dumps(body),
    }
    attachment_id = uuid.uuid4()
    attachment["id"] = str(attachment_id)
    message["body"]["content"] = f"<attachment id='{attachment_id}'></attachment>"
    message["attachments"].append(attachment)
    return message


def handle_teams_response(
    response: requests.Response,
    tenant_id: Optional[str] = None,
    channel_id: Optional[str] = None,
) -> None:
    """
    Check MS Teams API response and raise appropriate exceptions.

    Args:
        response: The HTTP response from MS Graph API
        tenant_id: Optional tenant identifier for context
        channel_id: Optional channel identifier for context

    Raises:
        RateLimitedError: If rate limited (429)
        ChannelNotFoundError: If channel not found (404)
        TeamsApiError: For other API errors
    """
    if response.status_code == 200 or response.status_code == 201:
        return

    error_body = response.text

    if response.status_code == 429:
        retry_after = 15
        try:
            retry_after = int(response.headers.get("Retry-After", 15))
        except (ValueError, TypeError):
            LOG.warning("Could not parse Retry-After header value, using default of %s seconds.", retry_after)

        raise RateLimitedError(
            message="MS Teams rate limit exceeded",
            channel="ms_teams",
            tenant_id=tenant_id,
            retry_after=retry_after,
        )

    if response.status_code == 404:
        raise ChannelNotFoundError(
            message="MS Teams channel not found",
            channel="ms_teams",
            tenant_id=tenant_id,
            channel_id=channel_id,
        )

    raise TeamsApiError(
        message=f"MS Teams API error: {response.status_code}",
        tenant_id=tenant_id,
        status_code=response.status_code,
        response_body=error_body,
    )


class MsTeamsClient:
    @staticmethod
    def list_joined_teams(access_token: str) -> List[Dict[str, Any]]:
        """List all joined Teams with their channels."""
        teams_response = requests.get(TEAMS_API_URL, headers={"Authorization": f"Bearer {access_token}"})
        if teams_response.status_code != 200:
            raise TeamsApiError(
                message="Failed to list joined teams",
                status_code=teams_response.status_code,
                response_body=teams_response.content.decode("utf-8"),
            )
        teams_data = teams_response.json()
        joined_teams = teams_data.get("value", [])

        teams_info = []
        for team in joined_teams:
            team_id = team.get("id")
            team_name = team.get("displayName")
            team_info = {"name": team_name, "id": team_id, "channels": []}

            # List channels in a specific team
            team_channels = MsTeamsClient.list_team_channels(access_token, team_id)
            team_info["channels"].extend(team_channels)

            teams_info.append(team_info)

        return teams_info

    @staticmethod
    def list_team_channels(access_token: str, team_id: str) -> List[Dict[str, Any]]:
        """List all channels in a team with pagination support."""
        validate_graph_api_id(team_id, "team_id")
        channels_api_url = f"https://graph.microsoft.com/v1.0/teams/{team_id}/channels"
        channels_info = []

        while channels_api_url:
            channels_response = requests.get(channels_api_url, headers={"Authorization": f"Bearer {access_token}"})
            channels_response.raise_for_status()

            channels_data = channels_response.json().get("value", [])
            for channel in channels_data:
                channel_id = channel.get("id")
                channel_name = channel.get("displayName")
                channel_info = {"name": channel_name, "id": channel_id}
                channels_info.append(channel_info)

            # Check for pagination
            next_link = channels_response.json().get("@odata.nextLink")
            channels_api_url = next_link if next_link else None

        return channels_info

    @staticmethod
    def post_to_ms_teams(
        access_token: str,
        ms_teams_channels: Any,
        message: Dict[str, Any],
        tenant: str,
        max_retries: int = settings.ms_teams.max_rate_limit_retries,
    ) -> Optional[requests.Response]:
        """
        Post a message to MS Teams channels.

        Args:
            access_token: MS Teams access token
            ms_teams_channels: Channel configuration (string, list, or dict)
            message: Message content as adaptive card
            tenant: Tenant identifier
            max_retries: Maximum retry attempts for rate limiting

        Returns:
            Response object from the API call

        Raises:
            DeliveryFailedError: If all retries are exhausted
        """
        attempts = 0
        headers = {"Authorization": f"Bearer {access_token}", "Content-Type": CONTENT_TYPE_JSON}

        if isinstance(ms_teams_channels, str):
            ms_teams_channels = json.loads(ms_teams_channels)
        if isinstance(ms_teams_channels, list):
            ms_teams_channels = ms_teams_channels[0]

        last_error = None
        team_id = ms_teams_channels.get("team_id")
        validate_graph_api_id(team_id, "team_id")

        for channel in json.loads(json.dumps(ms_teams_channels.get("channels"))):
            channel_id = channel.get("id")
            validate_graph_api_id(channel_id, "channel_id")

            send_message_api_url = GRAPH_API_BASE_URL + f"/v1.0/teams/{team_id}/channels/{channel_id}/messages"
            while True:
                response = requests.post(send_message_api_url, headers=headers, json=create_teams_message(message))
                LOG.debug(f"MS Teams notification sent for tenant {tenant}")

                if response.status_code == 429 and attempts < max_retries:
                    retry_after = int(response.headers.get("Retry-After", 15))
                    LOG.warning(f"MS Teams Rate limited for tenant {tenant}. Retrying after {retry_after}s...")
                    attempts += 1
                    last_error = RateLimitedError(
                        message="MS Teams rate limit exceeded",
                        channel="ms_teams",
                        tenant_id=tenant,
                        retry_after=retry_after,
                    )
                    time.sleep(retry_after)
                else:
                    if response.status_code >= 400:
                        payload = create_teams_message(message)
                        LOG.warning(
                            "MS Teams delivery failed: %s - %s | payload: %s",
                            response.status_code,
                            response.text,
                            json.dumps(payload, default=str),
                        )
                    return response

        if last_error:
            raise DeliveryFailedError(
                message="MS Teams delivery failed after retries",
                channel="ms_teams",
                tenant_id=tenant,
                attempts=attempts,
                last_error=last_error,
            )
        return None

    @staticmethod
    def set_reaction(
        access_token: str,
        team_id: str,
        channel_id: str,
        message_id: str,
        reaction_type: str,
        tenant_id: Optional[str] = None,
        max_retries: int = settings.ms_teams.max_rate_limit_retries,
    ) -> Dict[str, Any]:
        """
        Add a reaction to a message in MS Teams.

        Args:
            access_token: MS Teams access token
            team_id: Team ID where the message exists
            channel_id: Channel ID where the message exists
            message_id: ID of the message to react to
            reaction_type: Unicode emoji (e.g., "👍", "❤️")
            tenant_id: Optional tenant identifier for context
            max_retries: Maximum retry attempts for rate limiting

        Returns:
            Dict with success status and details
        """
        # Validate IDs to prevent path traversal attacks
        validate_graph_api_id(team_id, "team_id")
        validate_graph_api_id(channel_id, "channel_id")
        validate_graph_api_id(message_id, "message_id")

        attempts = 0
        headers = {"Authorization": f"Bearer {access_token}", "Content-Type": CONTENT_TYPE_JSON}

        reaction_url = (
            f"{GRAPH_API_BASE_URL}/v1.0/teams/{team_id}/channels/{channel_id}/messages/{message_id}/setReaction"
        )

        payload = {"reactionType": reaction_type}

        while attempts <= max_retries:
            response = requests.post(reaction_url, headers=headers, json=payload)

            # Success - MS Graph API returns 200, 201, or 204 for successful reactions
            if response.status_code in (200, 201, 204):
                LOG.debug("MS Teams reaction '%s' added to message %s", reaction_type, message_id)
                return {
                    "success": True,
                    "provider": "ms_teams",
                    "message_id": message_id,
                    "reaction": reaction_type,
                }

            # Rate limited - retry if attempts remaining
            if response.status_code == 429 and attempts < max_retries:
                retry_after = int(response.headers.get("Retry-After", 15))
                LOG.warning("MS Teams Rate limited. Retrying after %ss...", retry_after)
                attempts += 1
                time.sleep(retry_after)
                continue

            # Non-retryable error or retries exhausted
            LOG.warning(
                "MS Teams reaction failed: status=%d, response=%s",
                response.status_code,
                response.text,
            )
            return {
                "success": False,
                "provider": "ms_teams",
                "error": f"Failed to add reaction: {response.status_code} - {response.text}",
            }

        # Retries exhausted (only reached if last attempt was a 429)
        return {
            "success": False,
            "provider": "ms_teams",
            "error": "Rate limit exceeded after max retries",
        }

    @staticmethod
    def post_reply_to_thread(
        access_token: str,
        team_id: str,
        channel_id: str,
        parent_message_id: str,
        message: Any,
        max_retries: int = settings.ms_teams.max_rate_limit_retries,
    ) -> requests.Response:
        """
        Post a reply to an existing message thread in MS Teams.

        Args:
            access_token: MS Teams access token
            team_id: Team ID where the message exists
            channel_id: Channel ID where the message exists
            parent_message_id: ID of the parent message to reply to
            message: Message content (can be string or adaptive card dict)
            max_retries: Maximum retry attempts for rate limiting

        Returns:
            Response object from the API call
        """
        # Validate IDs to prevent path traversal attacks
        validate_graph_api_id(team_id, "team_id")
        validate_graph_api_id(channel_id, "channel_id")
        validate_graph_api_id(parent_message_id, "parent_message_id")

        attempts = 0
        headers = {"Authorization": f"Bearer {access_token}", "Content-Type": CONTENT_TYPE_JSON}

        reply_url = (
            f"{GRAPH_API_BASE_URL}/v1.0/teams/{team_id}/channels/{channel_id}/messages/{parent_message_id}/replies"
        )

        # Prepare message payload
        if isinstance(message, dict):
            payload = create_teams_message(message)
        else:
            payload = {"body": {"contentType": "text", "content": message}}

        while True:
            response = requests.post(reply_url, headers=headers, json=payload)
            LOG.debug(f"MS Teams threaded reply sent to message {parent_message_id}")

            if response.status_code == 429 and attempts < max_retries:
                retry_after = int(response.headers.get("Retry-After", 15))
                LOG.warning(f"MS Teams Rate limited. Retrying after {retry_after}s...")
                attempts += 1
                time.sleep(retry_after)
            else:
                return response

    @staticmethod
    def list_users(access_token: str) -> List[Dict[str, Any]]:
        """
        List users in the organization using MS Graph API.

        Args:
            access_token: MS Graph access token

        Returns:
            List of user dictionaries with id, name, email, and display_name
        """
        users_url = f"{GRAPH_API_BASE_URL}/v1.0/users"
        headers = {"Authorization": f"Bearer {access_token}"}
        users = []

        while users_url:
            response = requests.get(
                users_url,
                headers=headers,
                params={"$select": "id,displayName,mail,userPrincipalName,accountEnabled"},
            )

            if response.status_code != 200:
                LOG.warning("Failed to list users: %s - %s", response.status_code, response.text)
                raise TeamsApiError(
                    message="Failed to list users",
                    status_code=response.status_code,
                    response_body=response.text,
                )

            data = response.json()
            for user in data.get("value", []):
                # Skip disabled users
                if not user.get("accountEnabled", True):
                    continue

                users.append(
                    {
                        "id": user.get("id"),
                        "name": user.get("userPrincipalName"),
                        "real_name": user.get("displayName"),
                        "display_name": user.get("displayName"),
                        "email": user.get("mail") or user.get("userPrincipalName"),
                    }
                )

            # Handle pagination
            users_url = data.get("@odata.nextLink")

        return users

    @staticmethod
    def _create_one_on_one_chat(headers: Dict[str, str], user_id: str, max_retries: int) -> Dict[str, Any]:
        """
        Create a 1:1 chat with a user using MS Graph API.

        Args:
            headers: HTTP headers including authorization
            user_id: The user's AAD object ID
            max_retries: Maximum retry attempts

        Returns:
            Dict with chat_id on success, or error details on failure
        """
        create_chat_url = f"{GRAPH_API_BASE_URL}/v1.0/chats"
        chat_payload = {
            "chatType": "oneOnOne",
            "members": [
                {
                    "@odata.type": "#microsoft.graph.aadUserConversationMember",
                    "roles": ["owner"],
                    "user@odata.bind": f"https://graph.microsoft.com/v1.0/users/{user_id}",
                }
            ],
        }

        for attempt in range(max_retries + 1):
            chat_response = requests.post(create_chat_url, headers=headers, json=chat_payload)

            if chat_response.status_code == 429 and attempt < max_retries:
                retry_after = int(chat_response.headers.get("Retry-After", 15))
                LOG.warning("MS Teams rate limited creating chat. Retrying after %ss...", retry_after)
                time.sleep(retry_after)
                continue

            if chat_response.status_code == 429:
                return {"success": False, "error": "Rate limit exceeded creating chat"}

            if chat_response.status_code not in (200, 201):
                LOG.warning("Failed to create chat: %s - %s", chat_response.status_code, chat_response.text)
                return {
                    "success": False,
                    "error": f"Failed to create chat: {chat_response.status_code} - {chat_response.text}",
                }

            chat_id = chat_response.json().get("id")
            if not chat_id:
                return {"success": False, "error": "Failed to get chat ID"}
            return {"success": True, "chat_id": chat_id}

        return {"success": False, "error": "Failed to create chat"}

    @staticmethod
    def _send_chat_message(headers: Dict[str, str], chat_id: str, message: str, max_retries: int) -> Dict[str, Any]:
        """
        Send a message to an existing chat.

        Args:
            headers: HTTP headers including authorization
            chat_id: The chat ID to send message to
            message: Message text to send
            max_retries: Maximum retry attempts

        Returns:
            Dict with message_id on success, or error details on failure
        """
        send_message_url = f"{GRAPH_API_BASE_URL}/v1.0/chats/{chat_id}/messages"
        message_payload = {"body": {"contentType": "text", "content": message}}

        for attempt in range(max_retries + 1):
            msg_response = requests.post(send_message_url, headers=headers, json=message_payload)

            if msg_response.status_code == 429 and attempt < max_retries:
                retry_after = int(msg_response.headers.get("Retry-After", 15))
                LOG.warning("MS Teams rate limited sending message. Retrying after %ss...", retry_after)
                time.sleep(retry_after)
                continue

            if msg_response.status_code == 429:
                return {"success": False, "error": "Rate limit exceeded sending message"}

            if msg_response.status_code not in (200, 201):
                LOG.warning("Failed to send message: %s - %s", msg_response.status_code, msg_response.text)
                return {
                    "success": False,
                    "error": f"Failed to send message: {msg_response.status_code} - {msg_response.text}",
                }

            return {"success": True, "message_id": msg_response.json().get("id")}

        return {"success": False, "error": "Failed to send message"}

    @staticmethod
    def create_chat_and_send_message(
        access_token: str,
        user_id: str,
        message: str,
        tenant_id: Optional[str] = None,
        max_retries: int = settings.ms_teams.max_rate_limit_retries,
    ) -> Dict[str, Any]:
        """
        Create a 1:1 chat with a user and send a message.

        Args:
            access_token: MS Graph access token
            user_id: The user's AAD object ID to chat with
            message: Message text to send
            tenant_id: Optional tenant identifier for context
            max_retries: Maximum retry attempts for rate limiting

        Returns:
            Dict with success status and message details
        """
        validate_graph_api_id(user_id, "user_id")
        headers = {"Authorization": f"Bearer {access_token}", "Content-Type": CONTENT_TYPE_JSON}

        # Create or get the 1:1 chat
        chat_result = MsTeamsClient._create_one_on_one_chat(headers, user_id, max_retries)
        if not chat_result.get("success"):
            return chat_result

        chat_id = chat_result["chat_id"]

        # Send message to the chat
        msg_result = MsTeamsClient._send_chat_message(headers, chat_id, message, max_retries)
        if not msg_result.get("success"):
            return msg_result

        LOG.info("Successfully sent DM to user %s via chat %s", user_id, chat_id)

        return {
            "success": True,
            "data": {
                "user_id": user_id,
                "chat_id": chat_id,
                "message_id": msg_result["message_id"],
                "platform": "ms_teams",
            },
        }
