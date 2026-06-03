import functools
import logging
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timedelta
from urllib.parse import urlparse, unquote

import psycopg2
from tornado.ioloop import IOLoop

from config import Configs
from utils.datetime_utils import utc_now

tp_executor = ThreadPoolExecutor(30)
CRED_REFRESH_TIME = 36000
logger = logging.getLogger(__name__)


class BaseController(object):
    def __init__(self):
        self._clickhouse_client = None

    @property
    def postgres_client(self):
        parsed_url = urlparse(Configs.COLLECTOR_DB_URL)
        conn = psycopg2.connect(
            database=parsed_url.path[1:],
            user=unquote(parsed_url.username) if parsed_url.username else None,
            password=unquote(parsed_url.password) if parsed_url.password else None,
            host=parsed_url.hostname,
            port=parsed_url.port,
        )
        return conn

    def get_agent_last_synced_from_db(self, account_id) -> datetime:
        cursor = self.postgres_client.cursor()
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
        conn = self.postgres_client
        cursor = conn.cursor()
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
    def __init__(self) -> None:
        self.cred_store = {}

    def check_time_threshold(self, key_timestamp: int):
        time_diff = int(datetime.utcnow().timestamp()) - key_timestamp
        return time_diff < CRED_REFRESH_TIME

    def check_key(self, key):
        if key in self.cred_store:
            if self.check_time_threshold(self.cred_store[key]["timestamp"]):
                return True
            return False
        else:
            return False

    def get_value(self, key):
        dict_value = self.cred_store.get(key, False)
        if dict_value:
            return dict_value.get("value", False)
        return False

    def save_value(self, key, value):
        value_dict = {}
        value_dict["timestamp"] = int(datetime.utcnow().timestamp())
        value_dict["value"] = value
        self.cred_store[key] = value_dict
