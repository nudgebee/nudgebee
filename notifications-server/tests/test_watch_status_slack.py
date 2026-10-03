"""Slack "watching…" indicator + watch-completion delivery.

Watch results never reached Slack: the notifier sent the conversation UUID, which
_parse_conversation can't route. llm-server now sends the routable session id plus
``watch_id`` / ``watch_registered``; these pin the notifications-server half.
"""

from unittest.mock import MagicMock

from notifications_server.services.cache import Cache
from notifications_server.services import events as events_module
from notifications_server.services.events import Events

WATCH_ID = "11111111-1111-1111-1111-111111111111"


class _FakeRedis:
    def __init__(self):
        self.store = {}

    def get(self, key):
        return self.store.get(key)

    def set(self, key, value, nx=False, ex=None):
        # Mirror redis-py: SET NX → None when the key exists, True when it sets.
        if nx and key in self.store:
            return None
        self.store[key] = value
        return True

    def delete(self, key):
        self.store.pop(key, None)

    def expire(self, key, ttl):
        pass

    def pipeline(self):
        return _FakePipeline(self.store)


class _FakePipeline:
    def __init__(self, store):
        self._store = store

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        return False

    def set(self, key, value):
        self._store[key] = value

    def expire(self, key, ttl):
        pass

    def execute(self):
        pass

    # update_event_entry drives an optimistic WATCH/MULTI transaction.
    def watch(self, key):
        pass

    def unwatch(self):
        pass

    def multi(self):
        pass

    def get(self, key):
        return self._store.get(key)


def _fake_cache():
    cache = Cache()
    cache.redis_client = _FakeRedis()
    cache._ensure_connection = lambda: None  # noqa: SLF001 - skip real connection
    return cache


class _FakePayload:
    # conversation_id / reply_ref are required on the real LLMResponse and are
    # read by handle_final_response's event-analysis guard and by
    # _mark_finding_card_status. Default to a Slack session key so neither the
    # EVENT_CONVERSATION_SESSION_PREFIX guard nor the finding-card path fires.
    def __init__(self, response="", watch_id=None, conversation_id="C1-1.0", reply_ref=None):
        self.response = response
        self.watch_id = watch_id
        self.conversation_id = conversation_id
        self.reply_ref = reply_ref


def _events_with(cache, common_service):
    # Bypass Events.__init__ (needs a real DB + slack app) — we only exercise
    # handlers that depend on cache + common_service.
    ev = Events.__new__(Events)
    ev.cache = cache
    ev.common_service = common_service
    return ev


# --------------------------------------------------------------------------- #
# Cache: watch_status round-trip
# --------------------------------------------------------------------------- #


def test_cache_watch_status_round_trip():
    cache = _fake_cache()
    assert cache.get_watch_status(WATCH_ID) is None

    ok = cache.cache_watch_status(
        WATCH_ID,
        {"channel_id": "C1", "thread_ts": "1.0", "status_msg_ts": "2.0", "team_id": "T1", "platform": "slack"},
    )
    assert ok is True

    stored = cache.get_watch_status(WATCH_ID)
    assert stored["status_msg_ts"] == "2.0"
    assert stored["channel_id"] == "C1"
    assert "timestamp" in stored

    assert cache.remove_watch_status(WATCH_ID) is True
    assert cache.get_watch_status(WATCH_ID) is None


def test_cache_watch_status_no_watch_id_is_noop():
    cache = _fake_cache()
    assert cache.cache_watch_status("", {"status_msg_ts": "2.0"}) is False
    assert cache.get_watch_status("") is None


# --------------------------------------------------------------------------- #
# handle_watch_registered
# --------------------------------------------------------------------------- #


def test_handle_watch_registered_defers_the_post():
    # Fires mid-turn, ~20s before the agent's answer — posting now would strand
    # "Watching…" above the message it refers to. Claim only; post comes later.
    cache = _fake_cache()
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.slack_reply_in_thread_as_blocks.assert_not_called()
    stored = cache.get_watch_status(WATCH_ID)
    assert stored["status_msg_ts"] == "PENDING"
    assert stored["channel_id"] == "C1"
    assert stored["thread_ts"] == "1.0"
    assert stored["team_id"] == "T1"


def test_indicator_posts_after_the_answer_and_records_its_ts():
    cache = _fake_cache()
    common = MagicMock()
    common.slack_reply_in_thread_as_blocks.return_value = "999.111"
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")
    # The turn's own final carries no watch_id; it flushes the deferred indicator.
    ev.handle_final_response(
        _FakePayload(response="Triggered the rerun.", watch_id=None),
        {"pending_watch_id": WATCH_ID},
        "C1",
        "1.0",
        "T1",
    )

    # Answer first, indicator second — that ordering is the whole point.
    assert common.slack_reply_in_thread.called
    common.slack_reply_in_thread_as_blocks.assert_called_once()
    assert cache.get_watch_status(WATCH_ID)["status_msg_ts"] == "999.111"


def test_indicator_not_posted_when_watch_already_finished():
    # A watch that beats the final response back clears its status entry; the
    # deferred post must not resurrect an indicator pointing at nothing.
    cache = _fake_cache()
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")
    cache.remove_watch_status(WATCH_ID)

    ev.handle_final_response(
        _FakePayload(response="answer", watch_id=None), {"pending_watch_id": WATCH_ID}, "C1", "1.0", "T1"
    )

    common.slack_reply_in_thread_as_blocks.assert_not_called()


def test_handle_watch_registered_is_idempotent():
    cache = _fake_cache()
    cache.cache_watch_status(WATCH_ID, {"status_msg_ts": "1.0", "channel_id": "C1"})
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.slack_reply_in_thread_as_blocks.assert_not_called()


def test_handle_watch_registered_without_watch_id_is_noop():
    cache = _fake_cache()
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=None), {}, "C1", "1.0", "T1")

    common.slack_reply_in_thread_as_blocks.assert_not_called()


# --------------------------------------------------------------------------- #
# handle_final_response — post result + retire placeholder vs plain reply
# --------------------------------------------------------------------------- #


def test_handle_final_response_posts_result_then_deletes_status_message():
    cache = _fake_cache()
    cache.cache_watch_status(
        WATCH_ID,
        {"channel_id": "C1", "thread_ts": "1.0", "status_msg_ts": "999.111", "team_id": "T1", "platform": "slack"},
    )
    common = MagicMock()
    common.delete_slack_message.return_value = True
    ev = _events_with(cache, common)

    ev.handle_final_response(
        _FakePayload(response="✅ Watch completed: rollout is done", watch_id=WATCH_ID), {}, "C1", "1.0", "T1"
    )

    # A chat.update fires no Slack notification, so the result is posted as a
    # new message (which pings) and the placeholder is deleted — never edited.
    common.update_slack_message_with_blocks.assert_not_called()
    common.slack_reply_in_thread.assert_called_once()
    common.delete_slack_message.assert_called_once_with("C1", "T1", "999.111")
    assert cache.get_watch_status(WATCH_ID) is None


def test_handle_final_response_deletes_status_only_after_posting():
    # Delete-then-failed-post would leave the user with nothing, so the delete
    # must not run when delivery raises.
    cache = _fake_cache()
    cache.cache_watch_status(
        WATCH_ID,
        {"channel_id": "C1", "thread_ts": "1.0", "status_msg_ts": "999.111", "team_id": "T1", "platform": "slack"},
    )
    common = MagicMock()
    common.slack_reply_in_thread.side_effect = RuntimeError("slack down")
    ev = _events_with(cache, common)

    ev.handle_final_response(_FakePayload(response="done", watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.delete_slack_message.assert_not_called()


def test_handle_final_response_without_watch_id_posts_fresh_reply():
    cache = _fake_cache()
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_final_response(_FakePayload(response="plain answer", watch_id=None), {}, "C1", "1.0", "T1")

    common.update_slack_message_with_blocks.assert_not_called()
    common.slack_reply_in_thread.assert_called_once()


def test_handle_final_response_watch_without_tracked_status_falls_back_to_reply():
    # watch_id but no tracked status (TTL expired) → still deliver as a reply.
    cache = _fake_cache()
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_final_response(_FakePayload(response="late result", watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.update_slack_message_with_blocks.assert_not_called()
    common.slack_reply_in_thread.assert_called_once()


def test_handle_final_response_pending_placeholder_falls_back_to_reply():
    # A "PENDING" placeholder (slot reserved, post failed) isn't editable.
    cache = _fake_cache()
    cache.cache_watch_status(WATCH_ID, {"status_msg_ts": "PENDING"})
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_final_response(_FakePayload(response="result", watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.update_slack_message_with_blocks.assert_not_called()
    common.slack_reply_in_thread.assert_called_once()


# --------------------------------------------------------------------------- #
# handle_watch_registered — Redis-health gate
# --------------------------------------------------------------------------- #


def test_handle_watch_registered_skips_when_redis_unavailable():
    # Placeholder unpersistable (Redis down) → skip posting, so we never orphan
    # an uneditable "Watching…" message.
    cache = Cache()
    cache.redis_client = None
    cache._ensure_connection = lambda: None  # noqa: SLF001 - stay disconnected
    common = MagicMock()
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")

    common.slack_reply_in_thread_as_blocks.assert_not_called()


def test_deferred_indicator_clears_claim_when_post_returns_no_ts():
    # Post "succeeds" w/ no ts → claim dropped, so the terminal callback falls
    # back to a plain reply instead of trying to delete a message that isn't there.
    cache = _fake_cache()
    common = MagicMock()
    common.slack_reply_in_thread_as_blocks.return_value = None
    ev = _events_with(cache, common)

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")
    ev.handle_final_response(
        _FakePayload(response="answer", watch_id=None), {"pending_watch_id": WATCH_ID}, "C1", "1.0", "T1"
    )

    common.slack_reply_in_thread_as_blocks.assert_called_once()
    assert cache.get_watch_status(WATCH_ID) is None


# --------------------------------------------------------------------------- #
# pending_watch_id lifecycle — driven through the REAL module-level event_cache
# --------------------------------------------------------------------------- #


def _events_with_shared_event_cache(monkeypatch):
    """Point Events.cache AND the module-level `event_cache` at one fake store.

    handle_watch_registered / _post_watch_indicator write `pending_watch_id`
    through the module-level `event_cache`, not `self.cache`. Tests that only
    swap `self.cache` and hand in a hand-built cached_entry dict cannot tell a
    write that lands from one that silently no-ops.
    """
    shared = _FakeRedis()
    cache = Cache()
    cache.redis_client = shared
    cache._ensure_connection = lambda: None  # noqa: SLF001
    ev_cache = Cache()
    ev_cache.redis_client = shared
    ev_cache._ensure_connection = lambda: None  # noqa: SLF001
    monkeypatch.setattr(events_module, "event_cache", ev_cache)
    common = MagicMock()
    common.slack_reply_in_thread_as_blocks.side_effect = ["999.111", "888.222"]
    return _events_with(cache, common), common, ev_cache


def test_pending_watch_id_is_cleared_after_the_indicator_posts(monkeypatch):
    # Regression: `update_event_entry(..., pending_watch_id=None)` silently
    # no-ops (Cache drops None values), so the marker survived and every later
    # turn in the thread posted another "🔭 Watching…", orphaning the previous.
    ev, common, ev_cache = _events_with_shared_event_cache(monkeypatch)
    ev_cache.cache_event_entry("1.0", {"channel_id": "C1", "team_id": "T1"})

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")
    assert ev_cache.get_event_entry("1.0").get("pending_watch_id") == WATCH_ID

    # Turn 1's final posts the deferred indicator and must retire the marker.
    ev.handle_final_response(
        _FakePayload(response="answer 1", watch_id=None), ev_cache.get_event_entry("1.0"), "C1", "1.0", "T1"
    )
    assert common.slack_reply_in_thread_as_blocks.call_count == 1
    assert "pending_watch_id" not in ev_cache.get_event_entry("1.0")

    # Turn 2 is an ordinary follow-up: no second indicator, no orphan.
    ev.handle_final_response(
        _FakePayload(response="answer 2", watch_id=None), ev_cache.get_event_entry("1.0"), "C1", "1.0", "T1"
    )
    assert common.slack_reply_in_thread_as_blocks.call_count == 1
    assert ev.cache.get_watch_status(WATCH_ID)["status_msg_ts"] == "999.111"


def test_failed_indicator_post_drops_the_claim_and_the_marker(monkeypatch):
    # A raising post must not leave status_msg_ts="PENDING" + pending_watch_id
    # behind, or every later turn retries it for the entry's full lifetime.
    ev, common, ev_cache = _events_with_shared_event_cache(monkeypatch)
    common.slack_reply_in_thread_as_blocks.side_effect = RuntimeError("slack down")
    ev_cache.cache_event_entry("1.0", {"channel_id": "C1", "team_id": "T1"})

    ev.handle_watch_registered(_FakePayload(watch_id=WATCH_ID), {}, "C1", "1.0", "T1")
    ev.handle_final_response(
        _FakePayload(response="answer", watch_id=None), ev_cache.get_event_entry("1.0"), "C1", "1.0", "T1"
    )

    assert "pending_watch_id" not in ev_cache.get_event_entry("1.0")
    assert ev.cache.get_watch_status(WATCH_ID) is None
