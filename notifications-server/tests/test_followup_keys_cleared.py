"""Regression test for the Slack follow-up keys never being cleared.

Bug: ``_submit_followup`` cleared the pending-followup keys with
``update_event_entry(..., agent_id=None, message_id=None, followup_msg_ts=None)``.
But ``update_event_entry`` drops ``None`` values (so other callers can do
partial updates), so the clear was a silent no-op. The keys, set on the first
follow-up, were never removed. Once the conversation turn reached a terminal
(COMPLETED) state, the next free-text @mention was still routed as a follow-up
to the finalized ``message_id``; the LLM skipped re-processing the terminal
turn and returned an empty ``final`` -> no Slack reply.

Fix: ``remove_event_keys`` deletes the keys outright. These tests pin:
  * ``update_event_entry`` cannot clear a key (documents why a new method exists)
  * ``remove_event_keys`` removes the named keys and preserves the rest
  * after clearing, the pending-followup predicate is falsy (new turn, not followup)
"""

import json
import sys
import types

# Bypass the heavy notifications_server/__init__.py (slack_bolt, msal, Postgres
# engine on import); we only need the Cache class for these unit tests.
_ROOT = "notifications_server"
if _ROOT not in sys.modules:
    sys.modules[_ROOT] = types.ModuleType(_ROOT)
    sys.modules[_ROOT].__path__ = [f"{__file__.rsplit('/', 2)[0]}/{_ROOT}"]  # noqa: SLF001

from notifications_server.services.cache import Cache  # noqa: E402

THREAD_TS = "C0AT6G26LUX-1779952241.483549"
PENDING_KEYS = ["followup_msg_ts", "followup_question", "agent_id", "message_id"]


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


class _FakeRedis:
    def __init__(self):
        self.store = {}

    def get(self, key):
        return self.store.get(key)

    def set(self, key, value):
        self.store[key] = value

    def expire(self, key, ttl):
        pass

    def pipeline(self):
        return _FakePipeline(self.store)


def _make_cache_with_entry(entry):
    cache = Cache()
    cache.redis_client = _FakeRedis()
    cache._ensure_connection = lambda: None  # noqa: SLF001 - skip real connection
    cache.redis_client.store[f"chat_event:{THREAD_TS}"] = json.dumps(entry)
    return cache


def _stored_entry(cache):
    return json.loads(cache.redis_client.store[f"chat_event:{THREAD_TS}"])


def test_update_event_entry_cannot_clear_a_key():
    # Documents the root cause: None values are dropped, so the old "clear" was a no-op.
    cache = _make_cache_with_entry({"agent_id": "a1", "session_id": "s1"})
    cache.update_event_entry(THREAD_TS, agent_id=None)
    assert _stored_entry(cache)["agent_id"] == "a1"


def test_remove_event_keys_clears_pending_and_preserves_rest():
    cache = _make_cache_with_entry(
        {
            "followup_msg_ts": "111.222",
            "followup_question": "Which account?",
            "agent_id": "2338d2c7",
            "message_id": "4cec4f27",
            "session_id": "C0AT6G26LUX-1779952241.483549",
            "account_id": "a2a30b02",
        }
    )

    assert cache.remove_event_keys(THREAD_TS, PENDING_KEYS) is True

    entry = _stored_entry(cache)
    for key in PENDING_KEYS:
        assert key not in entry
    # Identity of the conversation must survive so the next turn reuses it.
    assert entry["session_id"] == "C0AT6G26LUX-1779952241.483549"
    assert entry["account_id"] == "a2a30b02"


def test_after_clear_next_mention_is_a_new_turn():
    # _has_pending_text_followup requires all three keys; after clearing it must be falsy
    # so the next @mention starts a fresh turn instead of binding to the terminal message.
    cache = _make_cache_with_entry({"followup_msg_ts": "111.222", "agent_id": "2338d2c7", "message_id": "4cec4f27"})
    cache.remove_event_keys(THREAD_TS, PENDING_KEYS)

    entry = _stored_entry(cache)
    has_pending = all(entry.get(k) for k in ("followup_msg_ts", "agent_id", "message_id"))
    assert has_pending is False


def test_remove_event_keys_no_entry_returns_false():
    cache = Cache()
    cache.redis_client = _FakeRedis()
    cache._ensure_connection = lambda: None  # noqa: SLF001
    assert cache.remove_event_keys(THREAD_TS, PENDING_KEYS) is False
