"""Standardized response constructors for notification delivery results."""

from notifications_server.schemas.message import PlatformResponse


def success_response(platform, channel_id=None, message_ts=None, team_id=None, **extra) -> dict:
    return PlatformResponse(
        platform=platform,
        status="success",
        channel_id=channel_id,
        message_ts=message_ts,
        team_id=team_id,
        **extra,
    ).model_dump(exclude_none=True)


def failed_response(platform, reason=None, **extra) -> dict:
    return PlatformResponse(
        platform=platform,
        status="failed",
        reason=reason,
        **extra,
    ).model_dump(exclude_none=True)


def system_response(status, reason=None) -> dict:
    return PlatformResponse(
        platform="system",
        status=status,
        reason=reason,
    ).model_dump(exclude_none=True)
