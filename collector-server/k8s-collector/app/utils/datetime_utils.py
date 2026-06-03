from datetime import datetime, timezone


def utc_now() -> datetime:
    # Naive UTC datetime — matches the tz-naive `timestamp` (no time zone)
    # columns used across the schema (e.g. k8s_pods/k8s_workloads/k8s_nodes
    # creation_time/last_seen, agent.last_synced_at). Built from a tz-aware
    # value so it is real UTC, not the process-local wall clock that a bare
    # datetime.now() would produce.
    return datetime.now(timezone.utc).replace(tzinfo=None)


def utc_from_epoch_millis(millis: float) -> datetime:
    # Convert epoch milliseconds to a naive-UTC datetime. datetime.fromtimestamp
    # without a tz argument interprets the epoch in the process-local zone and
    # yields local wall-clock time, which then gets stored as if it were UTC in
    # the no-tz columns. Anchoring to timezone.utc fixes that.
    return datetime.fromtimestamp(millis / 1000, tz=timezone.utc).replace(tzinfo=None)


def utc_from_epoch_seconds(seconds: float) -> datetime:
    # Same as utc_from_epoch_millis but for epoch seconds.
    return datetime.fromtimestamp(seconds, tz=timezone.utc).replace(tzinfo=None)
