from datetime import datetime, timezone


def utc_now() -> datetime:
    # Naive UTC datetime — matches the tz-naive DateTime columns used across the
    # notifications-server schema. Built from a tz-aware value to avoid the
    # S6903 / deprecated-utcnow pitfall on Python 3.12+.
    return datetime.now(timezone.utc).replace(tzinfo=None)
