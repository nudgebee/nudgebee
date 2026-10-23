"""Why a send to one destination failed, as one of a fixed set of reasons, and
whether sending again can help. Reasons come from each platform's own error
code or HTTP status, never from message text."""

import json
from typing import Any, Dict, Optional, Set, Tuple

from notifications_server.services.response_helpers import failed_response

# Sending again cannot help until someone changes the setup.
CHANNEL_NOT_FOUND = "channel_not_found"
NOT_IN_CHANNEL = "not_in_channel"
AUTH_FAILED = "auth_failed"
PERMISSION_DENIED = "permission_denied"
WEBHOOK_GONE = "webhook_gone"
MESSAGE_REJECTED = "message_rejected"
NOT_CONFIGURED = "not_configured"
INVALID_REQUEST = "invalid_request"

# Sending again may work.
RATE_LIMITED = "rate_limited"
PLATFORM_UNAVAILABLE = "platform_unavailable"
INTERNAL_ERROR = "internal_error"

# Reason of a `not_sent` result: the tenant has nowhere to send it.
NO_DESTINATION = "no_destination"

RETRYABLE = frozenset({RATE_LIMITED, PLATFORM_UNAVAILABLE, INTERNAL_ERROR})

WEBHOOK_PLATFORMS = frozenset({"slack_webhook", "ms_teams_webhook", "google_chat_webhook"})

_SLACK_ERRORS = {
    "channel_not_found": CHANNEL_NOT_FOUND,
    "is_archived": CHANNEL_NOT_FOUND,
    "not_in_channel": NOT_IN_CHANNEL,
    "user_not_in_channel": NOT_IN_CHANNEL,
    "invalid_auth": AUTH_FAILED,
    "not_authed": AUTH_FAILED,
    "account_inactive": AUTH_FAILED,
    "token_revoked": AUTH_FAILED,
    "token_expired": AUTH_FAILED,
    "not_allowed_token_type": AUTH_FAILED,
    "org_login_required": AUTH_FAILED,
    "two_factor_setup_required": AUTH_FAILED,
    "missing_scope": PERMISSION_DENIED,
    "access_denied": PERMISSION_DENIED,
    "no_permission": PERMISSION_DENIED,
    "team_access_not_granted": PERMISSION_DENIED,
    "enterprise_is_restricted": PERMISSION_DENIED,
    "restricted_action": PERMISSION_DENIED,
    "restricted_action_read_only_channel": PERMISSION_DENIED,
    "restricted_action_thread_only_channel": PERMISSION_DENIED,
    "restricted_action_non_threadable_channel": PERMISSION_DENIED,
    "restricted_action_thread_locked": PERMISSION_DENIED,
    "posting_to_general_channel_denied": PERMISSION_DENIED,
    "messages_tab_disabled": PERMISSION_DENIED,
    "ekm_access_denied": PERMISSION_DENIED,
    "msg_too_long": MESSAGE_REJECTED,
    "no_text": MESSAGE_REJECTED,
    "invalid_blocks": MESSAGE_REJECTED,
    "invalid_blocks_format": MESSAGE_REJECTED,
    "invalid_attachments": MESSAGE_REJECTED,
    "too_many_attachments": MESSAGE_REJECTED,
    "attachment_payload_limit_exceeded": MESSAGE_REJECTED,
    "invalid_metadata_format": MESSAGE_REJECTED,
    "invalid_metadata_schema": MESSAGE_REJECTED,
    "metadata_too_large": MESSAGE_REJECTED,
    "invalid_arguments": MESSAGE_REJECTED,
    "invalid_arg_name": MESSAGE_REJECTED,
    "invalid_array_arg": MESSAGE_REJECTED,
    "invalid_charset": MESSAGE_REJECTED,
    "invalid_form_data": MESSAGE_REJECTED,
    "cannot_reply_to_message": MESSAGE_REJECTED,
    "thread_not_found": MESSAGE_REJECTED,
    "message_not_found": MESSAGE_REJECTED,
    "ratelimited": RATE_LIMITED,
    "rate_limited": RATE_LIMITED,
}

_GOOGLE_CHAT_ERRORS = {
    "PERMISSION_DENIED": PERMISSION_DENIED,
    "NOT_FOUND": CHANNEL_NOT_FOUND,
    "UNAUTHENTICATED": AUTH_FAILED,
    "INVALID_ARGUMENT": MESSAGE_REJECTED,
    "FAILED_PRECONDITION": MESSAGE_REJECTED,
    "rate_limit_exceeded": RATE_LIMITED,
}

_DISCORD_ERRORS = {
    10003: CHANNEL_NOT_FOUND,  # Unknown Channel
    50001: PERMISSION_DENIED,  # Missing Access
    50013: PERMISSION_DENIED,  # Missing Permissions
    50035: MESSAGE_REJECTED,  # Invalid Form Body
}


def failure(platform: str, reason: str, error_code: Optional[str] = None, **extra: Any) -> Dict[str, Any]:
    """A failed result carrying its reason, the platform's own code, and whether
    a retry may help."""
    return failed_response(platform, reason=reason, error_code=error_code, retryable=reason in RETRYABLE, **extra)


def http_reason(status: Optional[int], gone: str = CHANNEL_NOT_FOUND) -> str:
    """The reason for an HTTP status; `gone` is what a 404 or 410 means here."""
    if status is None or status >= 500 or status == 408:
        return PLATFORM_UNAVAILABLE
    if status == 429:
        return RATE_LIMITED
    if status == 401:
        return AUTH_FAILED
    if status == 403:
        return PERMISSION_DENIED
    if status in (404, 410):
        return gone
    if 400 <= status < 500:
        return MESSAGE_REJECTED
    return PLATFORM_UNAVAILABLE


def slack_failure(response: Any, **extra: Any) -> Dict[str, Any]:
    """A failed Slack result from the response Slack returned, or None when no
    response arrived. Slack answers an API error with HTTP 200 and its own
    code; under any other status the body may not be Slack's (slack_sdk puts
    its own message in `error` for one that isn't JSON), so the status decides."""
    data = getattr(response, "data", None)
    code = data.get("error") if isinstance(data, dict) else None
    status = getattr(response, "status_code", None)
    if code in _SLACK_ERRORS or (code and status == 200):
        return failure("slack", _SLACK_ERRORS.get(code, PLATFORM_UNAVAILABLE), error_code=code, **extra)
    return failure("slack", http_reason(status), error_code=str(status) if status else None, **extra)


def webhook_failure(platform: str, response: Any, **extra: Any) -> Dict[str, Any]:
    """A failed incoming-webhook result. A webhook that answers 403, 404 or 410
    has been removed or disabled."""
    status = getattr(response, "status_code", None)
    reason = WEBHOOK_GONE if status in (403, 404, 410) else http_reason(status)
    return failure(platform, reason, error_code=str(status) if status else None, **extra)


def teams_failure(error: Exception, **extra: Any) -> Dict[str, Any]:
    """A failed Teams bot result from the TeamsBotError the bot sender raised.
    Teams answers a throttled bot with 412 as well as 429."""
    from notifications_server.services.ms_teams.bot_sender import AppNotInTeamError, ChannelNotInTeamError

    status = getattr(error, "status", None)
    if isinstance(error, AppNotInTeamError):
        reason = NOT_IN_CHANNEL
    elif isinstance(error, ChannelNotInTeamError):
        reason = CHANNEL_NOT_FOUND
    elif status == 412:
        reason = RATE_LIMITED
    else:
        reason = http_reason(status)
    return failure("ms_teams", reason, error_code=str(status) if status else None, **extra)


def google_chat_failure(result: Dict[str, Any], **extra: Any) -> Dict[str, Any]:
    """A failed Google Chat app result from the client's result dict."""
    code = result.get("reason")
    if code in _GOOGLE_CHAT_ERRORS:
        reason = _GOOGLE_CHAT_ERRORS[code]
    elif result.get("retryable") is False:
        reason = MESSAGE_REJECTED
    else:
        reason = PLATFORM_UNAVAILABLE
    return failure("google_chat", reason, error_code=code, **extra)


def discord_failure(result: Dict[str, Any], **extra: Any) -> Dict[str, Any]:
    """A failed Discord result from the client's result dict, which carries the
    HTTP status and Discord's own error code."""
    code = result.get("code")
    status = result.get("status")
    reason = _DISCORD_ERRORS.get(code) or http_reason(status)
    error_code = str(code) if code else (str(status) if status else None)
    return failure("discord", reason, error_code=error_code, **extra)


def _channel_of(channel: Any) -> Any:
    if isinstance(channel, str):
        try:
            channel = json.loads(channel)
        except (TypeError, ValueError):
            return channel
    if isinstance(channel, list):
        return channel[0] if channel else None
    return channel


def delivery_channel_id(ip: Any, channel: Any) -> Optional[str]:
    """The channel a send through installation `ip` to `channel` goes to, as the
    sender reports it in `channel_id`. A webhook target is its own channel."""
    if getattr(ip, "platform", None) in WEBHOOK_PLATFORMS:
        return str(ip.id) if getattr(ip, "id", None) else None
    channel = _channel_of(channel)
    if isinstance(channel, dict) and getattr(ip, "platform", None) == "ms_teams" and "channels" in channel:
        channels = channel["channels"]
        channel = channels[0] if isinstance(channels, list) and channels else None
    if isinstance(channel, dict):
        channel = channel.get("id")
    return str(channel) if channel else None


def destination_fields(ip: Any, channel: Any = None) -> Dict[str, Any]:
    """The destination a result is about: the installation and the channel."""
    destination_id = getattr(ip, "id", None)
    return {
        "destination_id": str(destination_id) if destination_id else None,
        "channel_id": delivery_channel_id(ip, getattr(ip, "to_channel", None) if channel is None else channel),
    }


def retry_destination(result: Dict[str, Any]) -> Dict[str, Any]:
    """The destination of a failed result, as a retry names it."""
    return {
        "platform": result.get("platform"),
        "destination_id": result.get("destination_id"),
        "channel_id": result.get("channel_id"),
    }


def retry_key(ip: Any, channel: Any) -> Tuple[Any, ...]:
    """A send through installation `ip` to `channel`, comparable with the
    destinations a retry names (see retry_keys)."""
    fields = destination_fields(ip, channel)
    return getattr(ip, "platform", None), fields["destination_id"], fields["channel_id"]


def retry_keys(retry_destinations: Any) -> Set[Tuple[Any, ...]]:
    return {
        (destination.get("platform"), destination.get("destination_id"), destination.get("channel_id"))
        for destination in retry_destinations or []
        if isinstance(destination, dict)
    }
