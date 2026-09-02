"""
Tests for routing a bound incident channel's @mention into the same
llm-server conversation the automated event-investigation used, instead of a
fresh unrelated chat session.

Covers, at the events.Events layer:
  * _bound_session_id       — unwraps the session id from channel_metadata
  * _resolve_mapped_account — surfaces it alongside the resolved account
  * _handle_selected_account — applies the "event-<session_id>" override,
    and leaves the default thread-scoped session_id alone when there's
    nothing bound
"""

import pytest

from notifications_server.services.events import EVENT_CONVERSATION_SESSION_PREFIX, Events


class _Mapping:
    def __init__(self, account_id, channel_metadata="{}", mapping_id="map-1"):
        self.id = mapping_id
        self.account_id = account_id
        self.channel_metadata = channel_metadata


class _FakeEventCache:
    def __init__(self):
        self.store = {}

    def get_event_entry(self, key):
        return self.store.get(key)

    def cache_event_entry(self, key, entry):
        self.store[key] = dict(entry)

    def update_event_entry(self, key, **kwargs):
        if key not in self.store:
            return False
        self.store[key].update({k: v for k, v in kwargs.items() if v is not None})
        return True


class _FakeCommonService:
    app_id = "app-1"

    def __init__(self):
        self.replies = []
        self.context_replies = []

    def slack_reply_in_thread(self, channel_id, team_id, thread_ts, message):
        self.replies.append((channel_id, team_id, thread_ts, message))

    def slack_reply_in_thread_with_context(self, channel_id, team_id, thread_ts, message, context):
        self.context_replies.append((channel_id, team_id, thread_ts, message, context))


@pytest.fixture
def events_svc():
    svc = Events.__new__(Events)
    svc.cache = _FakeEventCache()
    svc.common_service = _FakeCommonService()
    return svc


# ------------------------------- _bound_session_id -------------------------------


def test_bound_session_id_extracts_value():
    mapping = _Mapping(account_id="acc-1", channel_metadata='{"session_id": "fp-123"}')
    assert Events._bound_session_id(mapping) == "fp-123"


def test_bound_session_id_none_when_metadata_empty():
    mapping = _Mapping(account_id="acc-1", channel_metadata="{}")
    assert Events._bound_session_id(mapping) is None


def test_bound_session_id_none_when_metadata_null():
    mapping = _Mapping(account_id="acc-1", channel_metadata=None)
    assert Events._bound_session_id(mapping) is None


def test_bound_session_id_none_on_malformed_json():
    mapping = _Mapping(account_id="acc-1", channel_metadata="not json")
    assert Events._bound_session_id(mapping) is None


# ----------------------------- _resolve_mapped_account -----------------------------


def test_resolve_mapped_account_includes_bound_session_id(monkeypatch, events_svc):
    mapping = _Mapping(account_id="acc-1", channel_metadata='{"session_id": "fp-abc"}')
    monkeypatch.setattr(events_svc, "_get_channel_account_mapping", lambda tenant_id, platform, channel_id: mapping)
    monkeypatch.setattr(events_svc, "_validate_user_access_to_account", lambda user_id, tenant_id, account_id: True)

    result = events_svc._resolve_mapped_account("slack", "C1", "user-1", ["tenant-1"])

    assert result == {"id": "acc-1", "bound_session_id": "fp-abc"}


def test_resolve_mapped_account_none_when_user_lacks_access(monkeypatch, events_svc):
    mapping = _Mapping(account_id="acc-1", channel_metadata='{"session_id": "fp-abc"}')
    monkeypatch.setattr(events_svc, "_get_channel_account_mapping", lambda tenant_id, platform, channel_id: mapping)
    monkeypatch.setattr(events_svc, "_validate_user_access_to_account", lambda user_id, tenant_id, account_id: False)

    assert events_svc._resolve_mapped_account("slack", "C1", "user-1", ["tenant-1"]) is None


def test_resolve_mapped_account_none_when_no_mapping(monkeypatch, events_svc):
    monkeypatch.setattr(events_svc, "_get_channel_account_mapping", lambda tenant_id, platform, channel_id: None)

    assert events_svc._resolve_mapped_account("slack", "C1", "user-1", ["tenant-1"]) is None


# ----------------------------- _handle_selected_account -----------------------------


def test_handle_selected_account_routes_to_bound_conversation(monkeypatch, events_svc):
    events_svc.cache.store["1700.001"] = {"text": "what's going on", "account_id": None, "tenant_id": None}
    monkeypatch.setattr(events_svc, "_fetch_and_update_account_details_by_id", lambda *a, **k: ("Acme Prod", "acc-1"))

    processed = []
    monkeypatch.setattr(
        events_svc, "_process_event", lambda channel_id, text, team_id, thread_ts, kind: processed.append(text)
    )

    events_svc._handle_selected_account({"id": "acc-1", "bound_session_id": "fp-xyz"}, "C1", "T1", "1700.001")

    expected_session_id = f"{EVENT_CONVERSATION_SESSION_PREFIX}fp-xyz"
    assert events_svc.cache.store["1700.001"]["session_id"] == expected_session_id
    assert processed == ["what's going on"]


def test_handle_selected_account_keeps_default_session_id_when_unbound(monkeypatch, events_svc):
    events_svc.cache.store["1700.002"] = {"text": "hello", "account_id": None, "tenant_id": None}
    monkeypatch.setattr(events_svc, "_fetch_and_update_account_details_by_id", lambda *a, **k: ("Acme Prod", "acc-1"))
    monkeypatch.setattr(events_svc, "_process_event", lambda *a, **k: None)

    # No bound_session_id — the plain "user picked from several accounts" case.
    events_svc._handle_selected_account({"id": "acc-1"}, "C1", "T1", "1700.002")

    # session_id key is never written to the cached entry in this path.
    assert "session_id" not in events_svc.cache.store["1700.002"]


def test_handle_selected_account_replies_session_expired_without_cached_entry(monkeypatch, events_svc):
    monkeypatch.setattr(events_svc, "_fetch_and_update_account_details_by_id", lambda *a, **k: ("Acme Prod", "acc-1"))

    events_svc._handle_selected_account({"id": "acc-1", "bound_session_id": "fp-xyz"}, "C1", "T1", "missing-thread")

    assert len(events_svc.common_service.replies) == 1


# ------------------------ _process_incident_channel_session_event ------------------------
# Redis fast path: /channels/join already cached account/tenant under the
# fingerprint-as-session_id key. A later @mention (any thread) should route
# straight into the "event-<fingerprint>" conversation without ever asking
# for an account.


def test_incident_channel_session_event_routes_to_event_conversation_when_account_known(monkeypatch, events_svc):
    fingerprint = "fp-known"
    events_svc.cache.store[fingerprint] = {"account_id": "acc-1", "tenant_id": "tenant-1"}

    processed = []
    monkeypatch.setattr(
        events_svc, "_process_event", lambda channel_id, text, team_id, thread_ts, kind: processed.append(text)
    )

    events_svc._process_incident_channel_session_event(
        channel_id="C1",
        context="ctx",
        event_id="slack-evt-1",
        incident_channel_session={"session_id": fingerprint},
        is_thread=False,
        slack_user_id="U1",
        team_id="T1",
        text="what happened?",
        thread_ts="1700.100",
        user_email="a@b.com",
        user_id="user-1",
    )

    # No account picker — straight to processing, and thread_ts now carries a
    # copy of the entry with the "event-" prefixed session_id.
    assert processed == ["what happened?"]
    assert events_svc.cache.store["1700.100"]["session_id"] == f"{EVENT_CONVERSATION_SESSION_PREFIX}{fingerprint}"
    assert len(events_svc.common_service.context_replies) == 1


def test_incident_channel_session_event_asks_for_account_when_unknown(monkeypatch, events_svc):
    fingerprint = "fp-unknown"
    events_svc.cache.store[fingerprint] = {"account_id": None, "tenant_id": None}

    confirmations = []
    monkeypatch.setattr(
        events_svc,
        "_request_cluster_confirmation",
        lambda channel_id, team_id, thread_ts, user_email, slack_user_id: confirmations.append(thread_ts),
    )
    monkeypatch.setattr(events_svc, "_process_event", lambda *a, **k: pytest.fail("should not process yet"))

    events_svc._process_incident_channel_session_event(
        channel_id="C1",
        context="ctx",
        event_id="slack-evt-2",
        incident_channel_session={"session_id": fingerprint},
        is_thread=False,
        slack_user_id="U1",
        team_id="T1",
        text="what happened?",
        thread_ts="1700.200",
        user_email="a@b.com",
        user_id="user-1",
    )

    assert confirmations == ["1700.200"]
