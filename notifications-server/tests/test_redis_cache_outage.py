"""
When Redis is unreachable the cache reads as a miss: rules, default policies and
installations come from the database, so notifications still go out and email still
honours a drop default. Those reads run off the event loop, a client created while
Redis is down is kept so it works once Redis is back, and a grouped alert that
cannot be queued is sent on its own.
"""

import asyncio
import socket
import threading
import uuid
from types import SimpleNamespace

import redis

from notifications_server.configs import settings
from notifications_server.models.models import NotificationDefaultPolicy, NotificationRules
from notifications_server.services import rules as rules_module
from notifications_server.services.cache import Cache
from notifications_server.services.message import GroupedMessageHandler, MessageService
from notifications_server.services.rules import NotificationRulesService

TENANT = "22222222-2222-2222-2222-222222222222"


class _DownRedis:
    """Every command fails the way redis-py does when the server is unreachable."""

    def __init__(self):
        self.threads = []

    def _fail(self, *_args, **_kwargs):
        self.threads.append(threading.get_ident())
        raise redis.exceptions.ConnectionError("Error 111 connecting to redis:6379. Connection refused.")

    get = set = delete = lpush = expire = llen = _fail

    def pipeline(self):
        return _DownPipeline(self)


class _DownPipeline:
    def __init__(self, client):
        self._client = client

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def set(self, *_args, **_kwargs):
        pass

    expire = delete = set

    def execute(self):
        self._client._fail()


def _down_cache():
    cache = Cache()
    cache.redis_client = _DownRedis()
    return cache


class _Rows:
    def __init__(self, rows):
        self._rows = rows

    def scalars(self):
        return self

    def all(self):
        return self._rows

    def __iter__(self):
        return iter(self._rows)


class _Session:
    def __init__(self, rows):
        self._rows = rows

    async def execute(self, statement, params=None):
        if "notification_rule_accounts" in str(statement):
            return _Rows([])
        return _Rows(self._rows)


def _rules_service(monkeypatch, rows):
    class _Factory:
        def __call__(self):
            return self

        async def __aenter__(self):
            return _Session(rows)

        async def __aexit__(self, *exc):
            return False

    monkeypatch.setattr(rules_module.BaseDB, "async_session", staticmethod(lambda engine: _Factory()))
    service = NotificationRulesService(engine=None)
    service.cache = _down_cache()
    return service


def test_cache_reads_are_misses_when_redis_is_down():
    cache = _down_cache()

    assert cache.get_installations(TENANT) is None
    assert cache.get_notification_rules(TENANT) is None
    assert cache.get_default_policies(TENANT) is None
    assert cache.get_event_entry("1700000000.000100") is None


def test_rules_load_from_the_database_off_the_event_loop(monkeypatch):
    rule = NotificationRules(id=uuid.uuid4(), tenant_id=TENANT, name="Prod", source="troubleshoot", is_active=True)
    service = _rules_service(monkeypatch, [rule])

    async def load():
        return threading.get_ident(), await service.get_notification_rules(TENANT)

    loop_thread, loaded = asyncio.run(load())

    assert loaded == [rule]
    # The read and the write-back both reached Redis, and neither ran on the loop.
    assert len(service.cache.redis_client.threads) == 2
    assert loop_thread not in service.cache.redis_client.threads


def test_a_drop_default_still_stops_email_when_redis_is_down(monkeypatch):
    from notifications_server.processors.notification import NotificationProcessor

    row = NotificationDefaultPolicy(tenant_id=TENANT, source="daily_recap", action="drop", mappings=[])
    service = _rules_service(monkeypatch, [row])

    class _Matcher:
        async def match_notification_rules(self, *_args):
            return [], "daily_recap"

    processor = NotificationProcessor.__new__(NotificationProcessor)
    processor.engine = None
    processor._rule_matcher = _Matcher()
    processor._rules_service = service

    assert asyncio.run(processor._get_configured_email_rules(TENANT, {"source": "daily_recap"})) is None


def test_installations_load_from_the_database_when_redis_is_down():
    installation = SimpleNamespace(platform="slack", to_dict=lambda: {"platform": "slack"})

    async def from_db(_session, tenant_id):
        return [installation]

    service = SimpleNamespace(cache=_down_cache(), get_installed_platforms=from_db)

    loaded = asyncio.run(MessageService.get_installed_platforms_from_cache_or_db(service, None, TENANT))

    assert loaded == [installation]


def test_a_grouped_alert_that_cannot_be_queued_is_sent_on_its_own():
    message_service = SimpleNamespace(_group_tasks={}, _flush_locks={})
    handler = GroupedMessageHandler(_down_cache(), message_service)

    result = asyncio.run(handler.enqueue({"tenant_id": TENANT, "type": "slo_alert", "parameters": {}}))

    # None sends it through the normal path rather than the digest.
    assert result is None
    assert message_service._group_tasks == {}


def test_a_client_created_while_redis_is_down_is_kept(monkeypatch):
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        free_port = sock.getsockname()[1]
    monkeypatch.setattr(settings.redis, "host", "127.0.0.1")
    monkeypatch.setattr(settings.redis, "port", free_port)

    client = Cache._create_redis_client()

    assert client is not None
    client.close()


def test_an_unexpected_ping_error_still_keeps_the_client(monkeypatch):
    def ping(_self):
        raise OSError("unexpected")

    monkeypatch.setattr(redis.Redis, "ping", ping)

    client = Cache._create_redis_client()

    assert client is not None
    client.close()
