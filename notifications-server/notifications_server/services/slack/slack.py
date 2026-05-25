from typing import Optional
from slack_sdk.errors import SlackApiError

from notifications_server.exceptions import (
    RateLimitedError,
    ChannelNotFoundError,
    ChannelArchivedError,
    SlackError,
)


def retriable_slack_api_error(exc: Exception) -> bool:
    """Check if a Slack API error is retriable (rate limited)."""
    if isinstance(exc, SlackApiError) and exc.response.headers.get("Retry-After"):
        return True
    return False


def slack_channel_not_found_error(exc: Exception) -> bool:
    """Check if error indicates channel not found."""
    if isinstance(exc, SlackApiError) and exc.response.data.get("error") == "channel_not_found":
        return True
    return False


def get_retry_after(exc: SlackApiError) -> Optional[int]:
    """Extract Retry-After header value from Slack API error."""
    retry_after = exc.response.headers.get("Retry-After")
    if retry_after:
        try:
            return int(retry_after)
        except (ValueError, TypeError):
            pass
    return None


def wrap_slack_error(
    exc: SlackApiError,
    tenant_id: Optional[str] = None,
    channel_id: Optional[str] = None,
) -> Exception:
    """
    Convert a SlackApiError to the appropriate notification exception.

    Args:
        exc: The original Slack API error
        tenant_id: Optional tenant identifier for context
        channel_id: Optional channel identifier for context

    Returns:
        A notification exception appropriate for the error type
    """
    error_code = exc.response.data.get("error", "unknown_error")

    # Rate limiting
    if retriable_slack_api_error(exc):
        retry_after = get_retry_after(exc)
        return RateLimitedError(
            message=f"Slack rate limit exceeded: {error_code}",
            channel="slack",
            tenant_id=tenant_id,
            retry_after=retry_after,
        )

    # Channel not found
    if error_code == "channel_not_found":
        return ChannelNotFoundError(
            message=f"Slack channel not found: {channel_id}",
            channel="slack",
            tenant_id=tenant_id,
            channel_id=channel_id,
        )

    # Channel archived
    if error_code == "is_archived":
        return ChannelArchivedError(
            message=f"Slack channel is archived: {channel_id}",
            channel="slack",
            tenant_id=tenant_id,
            channel_id=channel_id,
        )

    # Generic Slack error
    return SlackError(
        message=f"Slack API error: {error_code}",
        tenant_id=tenant_id,
        slack_error_code=error_code,
    )
