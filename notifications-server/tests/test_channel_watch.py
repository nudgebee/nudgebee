import pytest
from slack_sdk.errors import SlackApiError

from notifications_server.configs.settings import settings
from notifications_server.services import channel_watch as cw
from notifications_server.services.cache import Cache


class _Install:
    def __init__(self, token="xoxb-test", team_id="T1"):
        self.token = token
        self.team_id = team_id


class _FakeSlackClient:
    def __init__(self, channel_info=None, list_pages=None, post_error=None, info_error=None):
        self.channel_info = channel_info or {}
        self.list_pages = list_pages or [{"channels": []}]
        self.post_error = post_error
        self.info_error = info_error
        self.info_calls = []
        self.join_calls = []
        self.post_calls = []
        self.deleted = []
        self._page = 0

    def conversations_info(self, *, token, channel_id, **kwargs):
        self.info_calls.append(channel_id)
        if self.info_error:
            raise self.info_error
        return {"channel": self.channel_info}

    def conversations_join(self, *, token, channel_id, **kwargs):
        self.join_calls.append(channel_id)
        return {"ok": True}

    def chat_post(self, *, token, channel_id, text, **kwargs):
        if self.post_error:
            raise self.post_error
        self.post_calls.append((channel_id, text))
        return {"ok": True, "ts": "1700000000.000100"}

    def chat_delete(self, *, token, channel_id, ts, **kwargs):
        self.deleted.append((channel_id, ts))
        return {"ok": True}

    def channels_list(self, token, team_id, cursor=None, **kwargs):
        page = self.list_pages[self._page]
        self._page += 1
        if isinstance(page, Exception):
            raise page
        return page


class _FakeCache:
    def __init__(self):
        self.added = []
        self.removed = []

    def add_watched_channel(self, platform, team_id, channel_id):
        self.added.append((platform, team_id, channel_id))
        return True

    def remove_watched_channel(self, platform, team_id, channel_id):
        self.removed.append((platform, team_id, channel_id))
        return True


@pytest.fixture
def svc():
    service = cw.ChannelWatchService.__new__(cw.ChannelWatchService)
    service.session = object()
    return service


@pytest.fixture
def fake_cache(monkeypatch):
    cache = _FakeCache()
    monkeypatch.setattr(cw, "cache", cache)
    return cache


def _allow(monkeypatch):
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", True)
    monkeypatch.setattr(cw, "is_feature_enabled", lambda *a, **k: True)
    monkeypatch.setattr(cw, "load_installations", lambda *a, **k: [_Install()])


def test_enable_joins_public_channel_and_posts_disclosure(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(channel_info={"name": "incidents", "is_member": False, "is_private": False})
    svc.slack_app = type("App", (), {"client": client})()

    upserts = []
    monkeypatch.setattr(
        cw.channel_watch_repository,
        "upsert_channel_watch",
        lambda session, **kw: upserts.append(kw) or {"team_id": kw["team_id"], "channel_id": kw["channel_id"]},
    )

    result = svc.enable_watch("t1", "C1", created_by="u1")

    assert "data" in result
    assert client.join_calls == ["C1"]
    assert upserts[0]["channel_name"] == "incidents"
    assert fake_cache.added == [("slack", "T1", "C1")]
    assert client.post_calls and client.post_calls[0][0] == "C1"


def test_enable_private_without_membership_errors(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(channel_info={"name": "secret", "is_member": False, "is_private": True})
    svc.slack_app = type("App", (), {"client": client})()

    called = []
    monkeypatch.setattr(cw.channel_watch_repository, "upsert_channel_watch", lambda *a, **k: called.append(1))

    result = svc.enable_watch("t1", "C2")

    assert "error" in result
    assert "Invite @Nubi" in result["error"]["message"]
    assert not client.join_calls
    assert not called
    assert not fake_cache.added


def test_enable_writes_nothing_when_disclosure_fails(monkeypatch, svc, fake_cache):
    # Disclosure is posted BEFORE the registry write; a failed post means no row,
    # no mirror entry — watching-without-disclosure must be unreachable.
    _allow(monkeypatch)
    client = _FakeSlackClient(
        channel_info={"name": "ops", "is_member": True, "is_private": False},
        post_error=SlackApiError("down", {"error": "fatal_error"}),
    )
    svc.slack_app = type("App", (), {"client": client})()

    upserts = []
    monkeypatch.setattr(cw.channel_watch_repository, "upsert_channel_watch", lambda *a, **k: upserts.append(1))

    result = svc.enable_watch("t1", "C3")

    assert "error" in result
    assert not upserts
    assert not fake_cache.added
    assert not fake_cache.removed


def test_enable_save_failure_deletes_disclosure(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(channel_info={"name": "ops", "is_member": True, "is_private": False})
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(cw.channel_watch_repository, "upsert_channel_watch", lambda session, **kw: None)

    result = svc.enable_watch("t1", "C3")

    assert "error" in result
    assert client.deleted == [("C3", "1700000000.000100")]
    assert not fake_cache.added


def test_enable_requires_team_id_with_multiple_workspaces(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    monkeypatch.setattr(cw, "load_installations", lambda *a, **k: [_Install(team_id="T1"), _Install(team_id="T2")])
    client = _FakeSlackClient(channel_info={"name": "ops", "is_member": True, "is_private": False})
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(
        cw.channel_watch_repository,
        "upsert_channel_watch",
        lambda session, **kw: {"team_id": kw["team_id"], "channel_id": kw["channel_id"]},
    )

    ambiguous = svc.enable_watch("t1", "C11")
    assert "error" in ambiguous
    assert "team_id" in ambiguous["error"]["message"]

    explicit = svc.enable_watch("t1", "C11", team_id="T2")
    assert explicit["data"]["team_id"] == "T2"


def test_disable_bypasses_gate_and_clears_mirror(monkeypatch, svc, fake_cache):
    # Turning OFF must work even when the kill switch / feature flag are off.
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", False)
    monkeypatch.setattr(cw, "is_feature_enabled", lambda *a, **k: False)
    monkeypatch.setattr(
        cw.channel_watch_repository,
        "disable_channel_watch",
        lambda session, **kw: {"team_id": "T1", "channel_id": kw["channel_id"], "enabled": False},
    )

    result = svc.disable_watch("t1", "C4")

    assert result["data"]["enabled"] is False
    assert fake_cache.removed == [("slack", "T1", "C4")]


def test_enable_blocked_by_global_kill_switch(monkeypatch, svc, fake_cache):
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", False)
    monkeypatch.setattr(cw, "is_feature_enabled", lambda *a, **k: True)

    result = svc.enable_watch("t1", "C5")

    assert "error" in result
    assert "disabled" in result["error"]["message"]


def test_enable_blocked_without_tenant_feature_flag(monkeypatch, svc, fake_cache):
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", True)
    monkeypatch.setattr(cw, "is_feature_enabled", lambda *a, **k: False)

    result = svc.enable_watch("t1", "C6")

    assert "error" in result
    assert "not enabled for this tenant" in result["error"]["message"]


def test_list_watchable_merges_watch_state(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(
        list_pages=[
            {
                "channels": [
                    {"id": "C1", "name": "incidents", "is_private": False, "is_member": True},
                    {"id": "C2", "name": "random", "is_private": False, "is_member": False},
                ],
                "response_metadata": {},
            }
        ]
    )
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(
        cw.channel_watch_repository,
        "list_channel_watches",
        lambda session, tenant_id: [
            {
                "team_id": "T1",
                "channel_id": "C1",
                "platform": "slack",
                "enabled": True,
                "retention_days": 30,
                "created_by": "u1",
                "updated_at": "2026-07-24T10:12:00",
            }
        ],
    )
    monkeypatch.setattr(cw.channel_watch_repository, "get_display_names", lambda session, ids: {"u1": "Dana"})

    result = svc.list_watchable_channels("t1")

    by_id = {c["id"]: c for c in result["data"]}
    assert by_id["C1"]["watched"] is True
    assert by_id["C1"]["retention_days"] == 30
    assert by_id["C1"]["watched_since"] == "2026-07-24T10:12:00"
    assert by_id["C1"]["watched_by"] == "Dana"
    assert by_id["C2"]["watched"] is False
    assert by_id["C2"]["watched_since"] is None
    assert result["team_id"] == "T1"
    assert result["partial"] is False


class _RateLimitedResponse:
    headers = {"Retry-After": "1"}


def test_list_watchable_marks_partial_on_rate_limit(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(
        list_pages=[
            {
                "channels": [{"id": "C1", "name": "incidents", "is_private": False, "is_member": True}],
                "response_metadata": {"next_cursor": "page2"},
            },
            SlackApiError("ratelimited", _RateLimitedResponse()),
        ]
    )
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(cw.channel_watch_repository, "list_channel_watches", lambda *a, **k: [])

    result = svc.list_watchable_channels("t1")

    assert result["partial"] is True
    assert [c["id"] for c in result["data"]] == ["C1"]


def test_list_watchable_surfaces_db_error(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient()
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(cw.channel_watch_repository, "list_channel_watches", lambda *a, **k: None)

    result = svc.list_watchable_channels("t1")

    assert "error" in result


def test_list_watchable_rejects_other_platforms(monkeypatch, svc, fake_cache):
    result = svc.list_watchable_channels("t1", platform="ms_teams")
    assert "error" in result


def test_enable_invisible_channel_maps_to_invite_message(monkeypatch, svc, fake_cache):
    # Private channels the bot isn't in are invisible: conversations.info raises
    # channel_not_found instead of returning is_private/is_member flags.
    _allow(monkeypatch)
    client = _FakeSlackClient(info_error=SlackApiError("nf", {"error": "channel_not_found"}))
    svc.slack_app = type("App", (), {"client": client})()

    called = []
    monkeypatch.setattr(cw.channel_watch_repository, "upsert_channel_watch", lambda *a, **k: called.append(1))

    result = svc.enable_watch("t1", "C10")

    assert "error" in result
    assert "invite @Nubi" in result["error"]["message"]
    assert not called
    assert not fake_cache.added


def test_list_watchable_scopes_team_id_to_tenant(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(list_pages=[{"channels": [], "response_metadata": {}}])
    svc.slack_app = type("App", (), {"client": client})()
    monkeypatch.setattr(cw.channel_watch_repository, "list_channel_watches", lambda *a, **k: [])

    assert "error" in svc.list_watchable_channels("t1", team_id="T-FOREIGN")
    assert svc.list_watchable_channels("t1", team_id="T1")["team_id"] == "T1"


def test_enable_rejects_team_id_outside_tenant(monkeypatch, svc, fake_cache):
    _allow(monkeypatch)
    client = _FakeSlackClient(channel_info={"name": "ops", "is_member": True, "is_private": False})
    svc.slack_app = type("App", (), {"client": client})()

    called = []
    monkeypatch.setattr(cw.channel_watch_repository, "upsert_channel_watch", lambda *a, **k: called.append(1))

    result = svc.enable_watch("t1", "C9", team_id="T-FOREIGN")

    assert "error" in result
    assert not called
    assert not client.info_calls


class _FakePipe:
    def __init__(self, store):
        self.store = store
        self.ops = []

    def __enter__(self):
        return self

    def __exit__(self, *args):
        return False

    def delete(self, key):
        self.ops.append(("delete", key))

    def sadd(self, key, *members):
        self.ops.append(("sadd", key, members))

    def execute(self):
        for op in self.ops:
            if op[0] == "delete":
                self.store.sets.pop(op[1], None)
            else:
                self.store.sets.setdefault(op[1], set()).update(op[2])
        self.ops = []


class _FakeRedis:
    def __init__(self):
        self.sets = {}

    def sadd(self, key, *members):
        self.sets.setdefault(key, set()).update(members)
        return len(members)

    def srem(self, key, *members):
        self.sets.get(key, set()).difference_update(members)
        return len(members)

    def sismember(self, key, member):
        return member in self.sets.get(key, set())

    def pipeline(self):
        return _FakePipe(self)


def test_cache_watched_set_roundtrip():
    cache = Cache.__new__(Cache)
    cache.redis_client = _FakeRedis()

    assert cache.add_watched_channel("slack", "T1", "C1") is True
    assert cache.is_channel_watched("slack", "T1", "C1") is True
    assert cache.is_channel_watched("slack", "T1", "C2") is False

    assert cache.remove_watched_channel("slack", "T1", "C1") is True
    assert cache.is_channel_watched("slack", "T1", "C1") is False

    assert cache.rebuild_watched_channels("slack", "T1", ["C7", "C8"]) is True
    assert cache.is_channel_watched("slack", "T1", "C7") is True
    assert cache.is_channel_watched("slack", "T1", "C8") is True

    assert cache.rebuild_watched_channels("slack", "T1", []) is True
    assert cache.is_channel_watched("slack", "T1", "C7") is False


def test_cache_watched_set_unavailable_returns_none():
    cache = Cache.__new__(Cache)
    cache.redis_client = None
    # _ensure_connection will try to reconnect; keep it a no-op for the test.
    cache._ensure_connection = lambda: None

    assert cache.is_channel_watched("slack", "T1", "C1") is None
    assert cache.add_watched_channel("slack", "T1", "C1") is False
