import pytest

from notifications_server.configs.settings import settings
from notifications_server.repositories import channel_message_repository
from notifications_server.services import channel_ingest as ci
from notifications_server.utils.secret_redaction import redact_secrets


@pytest.fixture
def svc(monkeypatch):
    service = ci.ChannelIngestService.__new__(ci.ChannelIngestService)
    service.session = object()
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", True)
    monkeypatch.setattr(settings.notifications, "channel_ingest_max_messages_per_hour", 500)
    return service


@pytest.fixture
def store(monkeypatch):
    """Captures repository calls so tests assert on what would be persisted."""
    calls = {"stored": [], "deleted": [], "tenants": ["t1"], "recent": 0}

    monkeypatch.setattr(ci.channel_message_repository, "list_watching_tenants", lambda *a, **k: calls["tenants"])
    monkeypatch.setattr(
        ci.channel_message_repository,
        "store_message",
        lambda session, **kw: calls["stored"].append(kw) or True,
    )
    monkeypatch.setattr(
        ci.channel_message_repository,
        "delete_message",
        lambda session, **kw: calls["deleted"].append(kw) or 1,
    )
    monkeypatch.setattr(ci.channel_message_repository, "count_recent_messages", lambda *a, **k: calls["recent"])
    monkeypatch.setattr(ci.cache, "is_channel_watched", lambda *a, **k: True)
    monkeypatch.setattr(ci.cache, "mark_event_seen", lambda *a, **k: True)
    return calls


def _msg(**overrides):
    event = {"type": "message", "channel": "C1", "user": "U1", "text": "deploying now", "ts": "1750000000.000100"}
    event.update(overrides)
    return event


def test_stores_a_normal_message(svc, store):
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is True
    assert len(store["stored"]) == 1
    stored = store["stored"][0]
    assert stored["channel_id"] == "C1"
    assert stored["message"] == "deploying now"
    assert stored["provider_message_id"] == "1750000000.000100"
    assert stored["thread_id"] is None


def test_drops_when_kill_switch_off(svc, store, monkeypatch):
    monkeypatch.setattr(settings.notifications, "channel_awareness_enabled", False)
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is False
    assert not store["stored"]


def test_drops_unwatched_channel_without_touching_storage(svc, store, monkeypatch):
    monkeypatch.setattr(ci.cache, "is_channel_watched", lambda *a, **k: False)
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is False
    assert not store["stored"]


def test_falls_back_to_registry_when_redis_unavailable(svc, store, monkeypatch):
    monkeypatch.setattr(ci.cache, "is_channel_watched", lambda *a, **k: None)
    store["tenants"] = []
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is False

    store["tenants"] = ["t1"]
    assert svc.handle_message_event(_msg(), "T1", "Ev2") is True


def test_drops_bot_messages(svc, store):
    assert svc.handle_message_event(_msg(bot_id="B123"), "T1", "Ev1") is False
    assert svc.handle_message_event(_msg(subtype="bot_message"), "T1", "Ev2") is False
    assert not store["stored"]


@pytest.mark.parametrize("subtype", ["channel_join", "channel_leave", "channel_topic", "pinned_item"])
def test_drops_noise_subtypes(svc, store, subtype):
    assert svc.handle_message_event(_msg(subtype=subtype), "T1", "Ev1") is False
    assert not store["stored"]


def test_dedups_slack_retries(svc, store, monkeypatch):
    seen = set()

    def mark(event_id, **kwargs):
        if event_id in seen:
            return False
        seen.add(event_id)
        return True

    monkeypatch.setattr(ci.cache, "mark_event_seen", mark)

    assert svc.handle_message_event(_msg(), "T1", "Ev1") is True
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is False
    assert len(store["stored"]) == 1


def test_redacts_secrets_before_storing(svc, store):
    svc.handle_message_event(_msg(text="token is xoxb-123456789012-abcdefghijklmno"), "T1", "Ev1")
    assert "xoxb-" not in store["stored"][0]["message"]
    assert "[redacted]" in store["stored"][0]["message"]


def test_writes_one_copy_per_watching_tenant(svc, store):
    store["tenants"] = ["t1", "t2"]
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is True
    assert [s["tenant_id"] for s in store["stored"]] == ["t1", "t2"]


def test_keeps_thread_broadcasts(svc, store):
    # A thread reply the author also sent to channel is real conversation.
    event = _msg(subtype="thread_broadcast", ts="1750000000.000200", thread_ts="1750000000.000100")
    assert svc.handle_message_event(event, "T1", "Ev1") is True
    assert store["stored"][0]["message"] == "deploying now"


def test_thread_reply_records_its_thread(svc, store):
    svc.handle_message_event(_msg(ts="1750000000.000200", thread_ts="1750000000.000100"), "T1", "Ev1")
    assert store["stored"][0]["thread_id"] == "1750000000.000100"


def test_deleted_message_removes_every_copy(svc, store):
    event = {"type": "message", "subtype": "message_deleted", "channel": "C1", "deleted_ts": "1750000000.000100"}
    assert svc.handle_message_event(event, "T1", "Ev1") is True
    assert store["deleted"][0]["provider_message_id"] == "1750000000.000100"
    assert not store["stored"]


def test_edited_message_updates_stored_copy(svc, store):
    event = {
        "type": "message",
        "subtype": "message_changed",
        "channel": "C1",
        "message": {"user": "U1", "text": "corrected text", "ts": "1750000000.000100"},
    }
    assert svc.handle_message_event(event, "T1", "Ev1") is True
    assert store["stored"][0]["message"] == "corrected text"
    assert store["stored"][0]["provider_message_id"] == "1750000000.000100"


def test_message_edited_to_empty_is_removed(svc, store):
    event = {
        "type": "message",
        "subtype": "message_changed",
        "channel": "C1",
        "message": {"user": "U1", "text": "   ", "ts": "1750000000.000100"},
    }
    assert svc.handle_message_event(event, "T1", "Ev1") is True
    assert store["deleted"][0]["provider_message_id"] == "1750000000.000100"
    assert not store["stored"]


def test_volume_circuit_breaker_stops_a_firehose_channel(svc, store, monkeypatch):
    monkeypatch.setattr(settings.notifications, "channel_ingest_max_messages_per_hour", 10)
    store["recent"] = 10
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is False
    assert not store["stored"]


def test_volume_limit_of_zero_disables_the_check(svc, store, monkeypatch):
    monkeypatch.setattr(settings.notifications, "channel_ingest_max_messages_per_hour", 0)
    store["recent"] = 10_000
    assert svc.handle_message_event(_msg(), "T1", "Ev1") is True


def test_drops_message_without_usable_timestamp(svc, store):
    assert svc.handle_message_event(_msg(ts="not-a-timestamp"), "T1", "Ev1") is False
    assert not store["stored"]


class _FakeResult:
    def __init__(self, rowcount):
        self.rowcount = rowcount


class _FakeSession:
    """Records how many delete batches the sweep issues."""

    def __init__(self, rowcounts):
        self.rowcounts = list(rowcounts)
        self.executed = 0
        self.commits = 0

    def execute(self, *args, **kwargs):
        self.executed += 1
        return _FakeResult(self.rowcounts.pop(0) if self.rowcounts else 0)

    def commit(self):
        self.commits += 1

    def rollback(self):
        pass


def test_retention_sweep_stops_when_a_batch_is_not_full():
    session = _FakeSession([5000, 5000, 12])
    total = channel_message_repository.delete_expired_messages(session, batch_limit=5000)
    assert total == 10012
    assert session.executed == 3
    assert session.commits == 3


def test_retention_sweep_is_capped_per_run():
    # A large backlog drains across sweeps rather than in one long-running pass.
    session = _FakeSession([5000] * 50)
    total = channel_message_repository.delete_expired_messages(session, batch_limit=5000, max_batches=3)
    assert session.executed == 3
    assert total == 15000


def test_retention_sweep_returns_what_it_deleted_before_failing(monkeypatch):
    session = _FakeSession([5000])

    original_execute = session.execute
    calls = {"n": 0}

    def flaky(*args, **kwargs):
        calls["n"] += 1
        if calls["n"] > 1:
            raise RuntimeError("connection lost")
        return original_execute(*args, **kwargs)

    session.execute = flaky
    total = channel_message_repository.delete_expired_messages(session, batch_limit=5000)
    assert total == 5000


@pytest.mark.parametrize(
    "text",
    [
        "xoxb-123456789012-abcdefghijklmnop",
        "AKIAIOSFODNN7EXAMPLE",
        "Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
        "postgres://user:hunter2@db.internal:5432/app",
        "api_key = sk-abcdefghijklmnop",
        "https://hooks.slack.com/services/T000/B000/XXXXXXXXXXXX",
    ],
)
def test_redaction_catches_known_credential_shapes(text):
    assert "[redacted]" in redact_secrets(text)


def test_redaction_leaves_ordinary_text_alone():
    text = "restarting the payments deployment in eu-west-1"
    assert redact_secrets(text) == text
