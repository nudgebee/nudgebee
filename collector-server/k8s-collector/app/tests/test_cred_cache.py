"""Agent credential cache — lifetime, eviction, and the lookup race.

The entry lifetime is a revocation window, not a performance knob: agent
credentials are rotated and agents deleted in api-server, which cannot notify
this process, so a revoked credential keeps authenticating until its entry ages
out. It used to age out after 10 hours and expired entries were never removed,
so every agent key the process ever saw stayed resident for its lifetime.

These exercise CredCache directly — no Tornado request or database needed.
"""

import os
import sys
from datetime import datetime
from unittest import mock

_ENV_DEFAULTS = {
    "ENV": "DEV",
    "COLLECTOR_MODE": "worker",
    "COLLECTOR_DB_URL": "postgresql://u:p@localhost:5432/db?sslmode=disable",
    "NUDGEBEE_ENCRYPTION_KEY": "test-key",
    "ACTION_API_SERVER_TOKEN": "",
    "SERVICE_API_SERVER_URL": "http://localhost:8000",
    "RABBIT_MQ_HOST": "localhost",
    "RABBIT_MQ_PORT": "5672",
    "RABBIT_MQ_USERNAME": "guest",
    "RABBIT_MQ_PASSWORD": "guest",
    "REDIS_SERVER_HOST": "localhost",
    "REDIS_SERVER_PORT": "6379",
    "REDIS_USER_NAME": "",
    "REDIS_USER_PASSWORD": "",
    "CLICKHOUSE_ENABLED": "false",
    "CLICKHOUSE_HOST": "",
    "CLICKHOUSE_USER": "",
    "CLICKHOUSE_PASSWORD": "",
}

for _k, _v in _ENV_DEFAULTS.items():
    os.environ.setdefault(_k, _v)

for _mod in ("redis", "psycopg2", "psycopg2.extras", "psycopg2.pool", "tornado", "tornado.ioloop"):
    sys.modules.setdefault(_mod, mock.MagicMock())

_APP_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), ".."))
if _APP_DIR not in sys.path:
    sys.path.insert(0, _APP_DIR)

import controllers.base as base  # noqa: E402
from controllers.base import CredCache  # noqa: E402


def _stamp(cache, key, value, age_seconds):
    """Plant an entry aged `age_seconds` in the past."""
    now = int(datetime.utcnow().timestamp())
    cache.cred_store[key] = {"timestamp": now - age_seconds, "value": value}


def test_fresh_entry_is_returned():
    cache = CredCache()
    cache.save_value(key="agent-key", value={"id": "acct", "tenant": "t"})
    assert cache.get_if_fresh("agent-key") == {"id": "acct", "tenant": "t"}


def test_absent_key_is_a_miss():
    assert CredCache().get_if_fresh("never-seen") is None


def test_entry_past_the_revocation_window_is_a_miss():
    cache = CredCache()
    _stamp(cache, "agent-key", {"id": "acct"}, base.CRED_REFRESH_TIME + 1)
    assert cache.get_if_fresh("agent-key") is None, "a revoked credential must stop authenticating"


def test_expired_entries_are_pruned_on_write():
    cache = CredCache()
    _stamp(cache, "stale-1", {"id": "a"}, base.CRED_REFRESH_TIME + 1)
    _stamp(cache, "stale-2", {"id": "b"}, base.CRED_REFRESH_TIME + 1)

    cache.save_value(key="fresh", value={"id": "c"})

    assert list(cache.cred_store) == ["fresh"], "aged-out entries must not accumulate"


def test_revocation_window_is_bounded_to_an_hour():
    """Pins the window itself: this is the blast radius of a rotated credential."""
    assert base.CRED_REFRESH_TIME <= 3600
