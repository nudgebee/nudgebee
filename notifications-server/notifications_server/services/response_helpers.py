"""Standardized response constructors for notification delivery results."""

from typing import Any, Optional

from notifications_server.schemas.message import PlatformResponse


def success_response(
    platform: str,
    channel_id: Optional[str] = None,
    message_ts: Optional[str] = None,
    team_id: Optional[str] = None,
    **extra: Any,
) -> dict[str, Any]:
    return PlatformResponse(
        platform=platform,
        status="success",
        channel_id=channel_id,
        message_ts=message_ts,
        team_id=team_id,
        **extra,
    ).model_dump(exclude_none=True)


def failed_response(platform: str, reason: Optional[str] = None, **extra: Any) -> dict[str, Any]:
    return PlatformResponse(
        platform=platform,
        status="failed",
        reason=reason,
        **extra,
    ).model_dump(exclude_none=True)


def system_response(status: str, reason: Optional[str] = None) -> dict[str, Any]:
    return PlatformResponse(
        platform="system",
        status=status,
        reason=reason,
    ).model_dump(exclude_none=True)
