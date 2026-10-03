import functools
import logging
import threading
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from datetime import datetime, timedelta

from tornado.ioloop import IOLoop

from db import database
from utils.datetime_utils import utc_now

tp_executor = ThreadPoolExecutor(30)

# How long a cached agent credential stays usable. This is a revocation window,
# not just a performance knob: agent credentials are rotated and agents deleted
# in api-server, which has no way to notify this process, so a revoked credential
# keeps authenticating for this long. It was 36000 (10 hours). One hour matches
# the worst case api-server already accepts for its own credential revocation
# (see the JWT TTL reasoning in services/user/session_revocation.go). The cost of
# the shorter window is one extra Postgres lookup per agent per hour -- bounded
# by the number of agents, not by request volume, since a miss re-caches.
CRED_REFRESH_TIME = 3600
logger = logging.getLogger(__name__)


class BaseController(object):
    def __init__(self):
        self._clickhouse_client = None

    @contextmanager
    def postgres_connection(self):
        """Borrow a connection from the shared pool and always return it.

        The previous ``postgres_client`` property opened a fresh, unmanaged
        ``psycopg2.connect()`` on every access that no caller ever closed, so
        each auth-cache miss / sync leaked a connection until PostgreSQL hit
        ``max_connections``. Routing through the shared ThreadedConnectionPool
        bounds usage and guarantees the connection is returned (rolled back on
        error so it isn't handed back to the pool in an aborted-transaction
        state).
        """
        conn = database.create_db_connection_pool().getconn()
        try:
            yield conn
        except Exception:
            try:
                conn.rollback()
            except Exception:
                pass
            raise
        finally:
            try:
                database.create_db_connection_pool().putconn(conn)
            except Exception:
                logger.exception("Failed to return connection to pool, closing it directly")
                try:
                    conn.close()
                except Exception:
                    pass

    def get_agent_last_synced_from_db(self, account_id) -> datetime:
        with self.postgres_connection() as conn:
            with conn.cursor() as cursor:
                cursor.execute("select last_synced_at from agent where cloud_account_id = %s", (account_id,))
                resp = cursor.fetchone()
        if resp and resp[0]:
            last_date = resp[0] + timedelta(minutes=1)
        else:
            # new account
            last_date = utc_now()
        last_date = last_date.replace(hour=0, minute=0, second=0, microsecond=0)
        logger.info(f"Got last sync from db {last_date}, account_id {account_id}")
        return last_date

    def update_agent_last_synced_in_db(self, account_id: str, new_date: datetime) -> None:
        with self.postgres_connection() as conn:
            with conn.cursor() as cursor:
                logger.info(f"Updating last sync to {new_date}, for account id {account_id}")
                cursor.execute(
                    "update agent set last_synced_at = %s where cloud_account_id = %s",
                    (new_date, account_id),
                )
            conn.commit()


class BaseAsyncControllerWrapper(object):
    """
    Used to wrap sync controller methods to return futures
    """

    def __init__(self, config_cl=None):
        self.config_cl = config_cl
        self.executor = tp_executor
        self._controller = None
        self.io_loop = IOLoop.current()

    @property
    def controller(self):
        if not self._controller:
            self._controller = self._get_controller_class()(self.config_cl)
        return self._controller

    def _get_controller_class(self):
        raise NotImplementedError

    def get_awaitable(self, meth_name, *args, **kwargs):
        method = getattr(self.controller, meth_name)
        return self.io_loop.run_in_executor(self.executor, functools.partial(method, *args, **kwargs))

    def __getattr__(self, name):
        def _missing(*args, **kwargs):
            return self.get_awaitable(name, *args, **kwargs)

        return _missing


class CredCache:
    """Caches agent credentials so every request does not hit Postgres.

    The entry lifetime is the revocation window: a rotated or deleted agent
    credential keeps authenticating until its cached entry ages out, because
    credentials are changed in api-server and nothing notifies this process.
    Nothing here can shorten that window after the fact, so CRED_REFRESH_TIME is
    the whole of the story -- see the constant.
    """

    def __init__(self) -> None:
        self.cred_store = {}
        # Reads and writes come from Tornado's ThreadPoolExecutor, so the prune
        # in save_value can run while another thread is reading. Guard both.
        self._lock = threading.Lock()

    def check_time_threshold(self, key_timestamp: int):
        time_diff = int(datetime.utcnow().timestamp()) - key_timestamp
        return time_diff < CRED_REFRESH_TIME

    def get_if_fresh(self, key):
        """Return the cached credential for key, or None if absent or aged out.

        A single lookup on purpose. The previous check_key()/get_value() pair
        left a window where an entry could be dropped between the two calls and
        the caller would read the miss as an invalid credential -- a spurious
        401. That could not happen while entries were never removed; it can now
        that save_value prunes.
        """
        with self._lock:
            entry = self.cred_store.get(key)
            if entry and self.check_time_threshold(entry["timestamp"]):
                return entry["value"]
            return None

    def save_value(self, key, value):
        now = int(datetime.utcnow().timestamp())
        with self._lock:
            # Drop aged-out entries. Nothing removed them before, so every agent
            # key the process ever saw stayed resident for its lifetime.
            for stale_key in [k for k, v in self.cred_store.items() if not self.check_time_threshold(v["timestamp"])]:
                del self.cred_store[stale_key]
            self.cred_store[key] = {"timestamp": now, "value": value}
