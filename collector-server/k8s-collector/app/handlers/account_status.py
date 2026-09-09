"""Ingestion gate for cloud accounts that have been disabled.

An agent whose cloud account is `status = 'disabled'` keeps publishing to the
shared `k8s_agent_*` queues -- nothing on the agent side knows the account was
turned off. Every message it publishes was then processed in full, and for
events that means the whole server-side enrichment path (trigger_investigation
-> eventrule playbook -> relay RPC back to that same account's agent). When a
disabled account's agent is also unresponsive, each of those relay calls hangs
for its full timeout while holding one of the few consumer slots, so the shared
queue backs up for every other tenant behind it.

The gate is fail-open on purpose: an unknown account, an unexpected status
value, or a database error all mean "ingest". Only an explicit 'disabled' drops
the message, so a lookup problem can never silently stop ingestion for a live
tenant.

Statuses are cached per account for CACHE_TTL_SECONDS. Re-enabling an account
therefore takes up to that long to take effect, and agent messages published in
that window are dropped -- acceptable for a manual, rare operation.
"""

import logging
import threading
import time

from db import database

logger = logging.getLogger(__name__)

DISABLED_STATUS = "disabled"
CACHE_TTL_SECONDS = 60

# cloud_account_id -> (expires_at_monotonic, is_disabled)
_cache: dict[str, tuple[float, bool]] = {}
_cache_lock = threading.Lock()


def _fetch_is_disabled(cloud_account_id: str) -> bool:
    rows = database.select_data("cloud_accounts", ["status"], {"id": cloud_account_id})
    if not rows:
        # No such account row. Fail open -- dropping traffic for an id we cannot
        # resolve would hide a real ingestion problem as "account disabled".
        return False
    return rows[0][0] == DISABLED_STATUS


def is_account_disabled(cloud_account_id: str) -> bool:
    """True only when the account is known and explicitly disabled."""
    if not cloud_account_id:
        return False

    now = time.monotonic()
    with _cache_lock:
        cached = _cache.get(cloud_account_id)
        if cached is not None and now < cached[0]:
            return cached[1]

    try:
        disabled = _fetch_is_disabled(cloud_account_id)
    except Exception:
        logger.exception(
            "Could not read cloud_accounts.status for %s; ingesting the message",
            cloud_account_id,
        )
        return False

    with _cache_lock:
        _cache[cloud_account_id] = (now + CACHE_TTL_SECONDS, disabled)
    return disabled


def reset_cache() -> None:
    """Drop the cached statuses. For tests."""
    with _cache_lock:
        _cache.clear()
