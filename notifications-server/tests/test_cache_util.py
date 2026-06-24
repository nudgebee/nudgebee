import pytest
from unittest.mock import patch

from notifications_server.utils.cache_util import EventCache


@pytest.fixture(autouse=True)
def reset_singleton():
    """Reset the EventCache singleton before each test."""
    EventCache._instance = None
    yield
    EventCache._instance = None


def test_singleton():
    cache1 = EventCache()
    cache2 = EventCache()
    assert cache1 is cache2
    assert cache1.cache is cache2.cache


def test_add_and_get_entry():
    cache = EventCache()
    cache.add_entry(
        thread_ts="1234.5678",
        event_id="evt_1",
        event_context="context_data",
        text="hello",
        user_id="U123",
        tenant_id="tenant_1",
        account_id="acc_1",
    )

    entry = cache.get_entry("1234.5678")
    assert entry is not None
    assert entry["event_id"] == "evt_1"
    assert entry["event_context"] == "context_data"
    assert entry["text"] == "hello"
    assert entry["user_id"] == "U123"
    assert entry["tenant_id"] == "tenant_1"
    assert entry["account_id"] == "acc_1"
    assert entry["conversation_id"] is None
    assert "timestamp" in entry

    # Non-existent entry
    assert cache.get_entry("non_existent") is None


def test_update_entry():
    cache = EventCache()
    cache.add_entry(
        thread_ts="1234.5678",
        event_id="evt_1",
        event_context="context_data",
        text="hello",
        user_id="U123",
    )

    # Update existing entry
    success = cache.update_entry(
        thread_ts="1234.5678",
        text="new text",
        user_id="U999",
        tenant_id="new_tenant",
        account_id="new_acc",
        conversation_id="conv_1",
    )
    assert success is True

    entry = cache.get_entry("1234.5678")
    assert entry["text"] == "new text"
    assert entry["user_id"] == "U999"
    assert entry["tenant_id"] == "new_tenant"
    assert entry["account_id"] == "new_acc"
    assert entry["conversation_id"] == "conv_1"

    # Update non-existent entry
    assert cache.update_entry("invalid_ts", text="fail") is False


def test_remove_entry():
    cache = EventCache()
    cache.add_entry("1234", "evt_1", "ctx", "text", "U1")

    # Remove existing
    assert cache.remove_entry("1234") is True
    assert cache.get_entry("1234") is None

    # Remove non-existent
    assert cache.remove_entry("1234") is False


@patch("notifications_server.utils.cache_util.time.time")
def test_expiry_and_cleanup_on_read(mock_time):
    cache = EventCache()

    # Start at time 1000
    mock_time.return_value = 1000
    cache.add_entry("fresh_ts", "evt_1", "ctx", "text", "U1")

    # Move forward exactly 7200 seconds (the limit)
    mock_time.return_value = 8200
    entry = cache.get_entry("fresh_ts")
    assert entry is not None  # Should still be valid at exactly 7200s difference

    # Move forward past the 7200 seconds limit
    mock_time.return_value = 8201
    entry = cache.get_entry("fresh_ts")
    assert entry is None  # Should be expired

    # Verify it was cleaned up from the internal dictionary
    assert "fresh_ts" not in cache.cache
