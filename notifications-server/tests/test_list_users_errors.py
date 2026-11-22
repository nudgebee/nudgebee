"""#41063: the Dm picker's user list fails loudly instead of answering an empty object."""

import asyncio
import json
import time
from types import SimpleNamespace
from unittest import mock

import pytest
from slack_sdk.errors import SlackApiError
from starlette.requests import Request

from notifications_server.routers import common as router
from notifications_server.routers.common import HasuraActionPayload
from notifications_server.services import cache as cache_module
from notifications_server.services import common as common_module
from notifications_server.services.cache import Cache
from notifications_server.services.common import CommonService

INSTALL = SimpleNamespace(token="xoxb-test", team_id="T1")
MEMBER = {"id": "U1", "name": "alice", "profile": {"display_name": "Alice"}}
ALICE = {"id": "U1", "name": "alice", "real_name": None, "display_name": "Alice", "email": None}


class _SlackResp(dict):
    def __init__(self, error, status_code=200, headers=None):
        super().__init__(ok=False, error=error)
        self.status_code = status_code
        self.headers = headers or {}


def _rate_limited(retry_after="30"):
    return SlackApiError("ratelimited", _SlackResp("ratelimited", 429, {"Retry-After": retry_after}))


def _service(users_list):
    service = CommonService.__new__(CommonService)
    service.session = mock.Mock()
    service.slack_app = SimpleNamespace(client=SimpleNamespace(users_list=users_list))
    return service


@pytest.fixture(autouse=True)
def _no_backoff():
    common_module._slack_users_backoff.clear()
    yield
    common_module._slack_users_backoff.clear()


@pytest.fixture
def fake_cache():
    fake = mock.Mock()
    fake.get_cached_slack_users.return_value = None
    with mock.patch.object(common_module, "cache", fake):
        yield fake


def _entry(users, age_seconds):
    return {"users": users, "cached_at": time.time() - age_seconds}


def _list(users_list):
    with mock.patch.object(common_module, "load_installation", return_value=INSTALL):
        return _service(users_list).list_users("slack", "tenant-1")


def test_a_rate_limited_user_list_is_an_error_not_an_empty_list(fake_cache):
    assert _list(mock.Mock(side_effect=_rate_limited())) == {
        "error": {"code": "rate_limited", "message": common_module.USER_LIST_RATE_LIMITED, "retry_after": 30}
    }


def test_a_rate_limit_serves_the_last_cached_list_even_when_stale(fake_cache):
    fake_cache.get_cached_slack_users.return_value = _entry([ALICE], age_seconds=3600)
    users_list = mock.Mock(side_effect=_rate_limited())
    assert _list(users_list) == {"data": [ALICE]}
    users_list.assert_called_once()
    fake_cache.cache_slack_users.assert_not_called()


def test_a_rate_limit_mid_pagination_fails_the_whole_call(fake_cache):
    first_page = {"members": [MEMBER], "response_metadata": {"next_cursor": "c2"}}
    users_list = mock.Mock(side_effect=[first_page, _rate_limited()])
    assert _list(users_list)["error"]["code"] == "rate_limited"
    fake_cache.cache_slack_users.assert_not_called()


def test_any_other_slack_failure_is_an_error_not_an_empty_object(fake_cache):
    users_list = mock.Mock(side_effect=SlackApiError("fatal_error", _SlackResp("fatal_error")))
    assert _list(users_list) == {"error": {"code": "provider_error", "message": common_module.USER_LIST_UNAVAILABLE}}


@pytest.mark.parametrize("error", sorted(common_module.SLACK_RECONNECT_ERRORS))
def test_auth_and_scope_failures_ask_for_a_reconnect(fake_cache, error):
    users_list = mock.Mock(side_effect=SlackApiError(error, _SlackResp(error)))
    assert _list(users_list) == {"error": {"code": "reconnect_required", "message": common_module.USER_LIST_RECONNECT}}


def test_after_a_rate_limit_slack_is_not_called_until_retry_after(fake_cache):
    users_list = mock.Mock(side_effect=_rate_limited("30"))
    assert _list(users_list)["error"]["code"] == "rate_limited"
    second = _list(users_list)
    assert second["error"]["code"] == "rate_limited" and 0 < second["error"]["retry_after"] <= 30
    users_list.assert_called_once()

    fake_cache.get_cached_slack_users.return_value = _entry([ALICE], age_seconds=3600)
    assert _list(users_list) == {"data": [ALICE]}
    users_list.assert_called_once()


def test_slack_is_called_again_once_retry_after_passes(fake_cache):
    users_list = mock.Mock(
        side_effect=[_rate_limited("30"), {"members": [MEMBER], "response_metadata": {"next_cursor": ""}}]
    )
    _list(users_list)
    common_module._slack_users_backoff[("tenant-1", "T1")] = time.time() - 1
    assert _list(users_list) == {"data": [ALICE]}


def test_an_unexpected_failure_is_an_error_not_an_empty_object(fake_cache):
    users_list = mock.Mock(side_effect=RuntimeError("boom"))
    assert _list(users_list) == {"error": {"code": "internal_error", "message": common_module.USER_LIST_UNAVAILABLE}}


def test_fresh_users_are_served_from_the_cache_without_calling_slack(fake_cache):
    fake_cache.get_cached_slack_users.return_value = _entry([ALICE], age_seconds=10)
    users_list = mock.Mock()
    assert _list(users_list) == {"data": [ALICE]}
    users_list.assert_not_called()
    fake_cache.get_cached_slack_users.assert_called_once_with("tenant-1", "T1")


def test_a_stale_cache_is_refreshed_from_slack(fake_cache):
    fake_cache.get_cached_slack_users.return_value = _entry([], age_seconds=cache_module.SLACK_USERS_FRESH_SECONDS + 1)
    users_list = mock.Mock(return_value={"members": [MEMBER], "response_metadata": {"next_cursor": ""}})
    assert _list(users_list) == {"data": [ALICE]}
    fake_cache.cache_slack_users.assert_called_once_with("tenant-1", "T1", [ALICE])


@pytest.mark.parametrize(
    "code,status",
    [("rate_limited", 429), ("provider_error", 502), ("reconnect_required", 502), ("internal_error", 500)],
)
def test_the_endpoint_answers_an_error_with_a_failing_status(monkeypatch, code, status):
    monkeypatch.setattr(router, "_list_users_sync", lambda platform, tenant: {"error": {"code": code, "message": "m"}})
    body = HasuraActionPayload.model_validate({"input": {"platform": "slack"}, "session_variables": {"tenant_id": "t"}})
    resp = asyncio.run(router.list_users(Request({"type": "http", "headers": []}), body))
    assert resp.status_code == status


def test_the_endpoint_runs_the_blocking_call_off_the_event_loop(monkeypatch):
    calls = []

    async def fake_threadpool(fn, *args):
        calls.append((fn, args))
        return {"data": []}

    monkeypatch.setattr(router, "run_in_threadpool", fake_threadpool)
    body = HasuraActionPayload.model_validate({"input": {"platform": "slack"}, "session_variables": {"tenant_id": "t"}})
    resp = asyncio.run(router.list_users(Request({"type": "http", "headers": []}), body))
    assert resp.status_code == 200
    assert calls == [(router._list_users_sync, ("slack", "t"))]


class _FakeRedis:
    def __init__(self):
        self.store, self.ttl = {}, {}

    def get(self, key):
        return self.store.get(key)

    def set(self, key, value, ex=None):
        self.store[key], self.ttl[key] = value, ex


def _cache(provider, redis_client=None):
    c = Cache.__new__(Cache)
    c.redis_client = redis_client
    patched = mock.patch.object(cache_module.settings.redis, "provider", provider)
    return c, patched


def test_the_user_list_cache_works_with_the_memory_provider():
    c, patched = _cache("memory")
    with patched, mock.patch.dict(cache_module._memory_slack_users, clear=True):
        assert c.get_cached_slack_users("tenant-1", "T1") is None
        c.cache_slack_users("tenant-1", "T1", [ALICE])
        assert c.get_cached_slack_users("tenant-1", "T1")["users"] == [ALICE]
        assert c.get_cached_slack_users("tenant-2", "T1") is None


def test_the_memory_cache_drops_entries_past_the_keep_window():
    c, patched = _cache("memory")
    with patched, mock.patch.dict(cache_module._memory_slack_users, clear=True):
        c.cache_slack_users("tenant-1", "T1", [ALICE])
        later = time.time() + cache_module.SLACK_USERS_KEEP_SECONDS + 1
        with mock.patch.object(cache_module.time, "time", return_value=later):
            assert c.get_cached_slack_users("tenant-1", "T1") is None


def test_the_user_list_cache_works_with_the_redis_provider():
    fake = _FakeRedis()
    c, patched = _cache("redis", fake)
    with patched, mock.patch.dict(cache_module._memory_slack_users, clear=True):
        c.cache_slack_users("tenant-1", "T1", [ALICE])
        assert cache_module._memory_slack_users == {}
        key = "slack_users_list:tenant-1:T1"
        assert json.loads(fake.store[key])["users"] == [ALICE]
        assert fake.ttl[key] == cache_module.SLACK_USERS_KEEP_SECONDS
        assert c.get_cached_slack_users("tenant-1", "T1")["users"] == [ALICE]
