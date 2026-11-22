"""Regression tests for _handle_event_conversation's channel_id/thread_ts
resolution, in priority order: reply_ref (real @mentions only) >
sent_notifications (finding-alert channels) > resolve_incident_channel_and_thread
(slack.join_channel-bound channels, #37885 -- only overrides on success, so a
finding-alert channel with no incident-cache entry is untouched)."""

from notifications_server.routers import llm_callbacks
from notifications_server.routers.llm_callbacks import LLMResponse, _handle_event_conversation

CONVERSATION_ID = "event-756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"


class _FakeCommonService:
    """Records whether the fingerprint-lookup / incident-cache paths were
    invoked, so tests can assert they're skipped once reply_ref is trusted."""

    def __init__(
        self,
        sent_notifications_result=(None, None, None, None),
        incident_resolve_result=(None, None, None, None),
    ):
        self.sent_notifications_result = sent_notifications_result
        self.incident_resolve_result = incident_resolve_result
        self.get_channel_and_ts_calls = []
        self.resolve_incident_calls = []

    def get_channel_and_ts_from_sent_notifications(self, conversation_id, tenant_id=None):
        self.get_channel_and_ts_calls.append((conversation_id, tenant_id))
        return self.sent_notifications_result

    def resolve_incident_channel_and_thread(self, incident_uuid):
        self.resolve_incident_calls.append(incident_uuid)
        return self.incident_resolve_result


class _NullContext:
    """Stands in for `with Session(sync_engine) as session:` in tests that
    never touch the DB session -- is_feature_enabled is mocked directly."""

    def __enter__(self):
        return None

    def __exit__(self, *args):
        return False


def test_handle_event_conversation_trusts_reply_ref_for_thread_but_uses_db_for_team_and_account(monkeypatch):
    # team_id/account_id are stable properties of a bound channel (which
    # workspace, which cloud account) that don't change per-question, so they
    # still come from the existing DB-backed lookup even when reply_ref
    # overrides channel_id/thread_ts with the thread this specific question
    # was actually asked from.
    common_service = _FakeCommonService(
        sent_notifications_result=(
            "STALE_CHANNEL",
            "STALE_THREAD",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        )
    )
    payload = LLMResponse(
        conversation_id=CONVERSATION_ID,
        type="final",
        response="2+2 equals 4",
        tenant_id=None,
        reply_ref="C0BRFTQCQ48-1786000000.000100",
    )

    channel_id, thread_ts, team_id, account_id = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    # channel_id/thread_ts come from reply_ref, not the (here deliberately
    # stale) fingerprint-lookup result.
    assert (channel_id, thread_ts) == ("C0BRFTQCQ48", "1786000000.000100")
    assert (team_id, account_id) == ("T05JWTTH3NH", "a2a30b02-0f67-42e5-a2ab-c658230fd798")
    # The lookup itself always runs (for team_id/account_id), but the
    # incident-cache fallback must still be skipped -- reply_ref signals an
    # ongoing follow-up, not the original investigation's own completion.
    assert common_service.get_channel_and_ts_calls == [(CONVERSATION_ID, None)]
    assert common_service.resolve_incident_calls == []


def test_handle_event_conversation_falls_back_when_reply_ref_absent(monkeypatch):
    # The original investigation's own completion never goes through
    # events.py:_process_event, so it never sets reply_ref -- must fall back
    # to the pre-existing lookup (and, if enabled, the announce path).
    common_service = _FakeCommonService(
        sent_notifications_result=(
            "C_ALERT",
            "1700000000.000000",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        )
    )
    payload = LLMResponse(conversation_id=CONVERSATION_ID, type="final", response="Investigation done", tenant_id=None)

    result = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    assert result == ("C_ALERT", "1700000000.000000", "T05JWTTH3NH", "a2a30b02-0f67-42e5-a2ab-c658230fd798")
    assert common_service.get_channel_and_ts_calls == [(CONVERSATION_ID, None)]


def test_handle_event_conversation_uses_incident_cache_when_reply_ref_absent(monkeypatch):
    # The dangerous failure mode isn't "routes to the wrong thread" -- it's
    # "silently drops a channel bound only via slack.join_channel's
    # bind_session_id, which sent_notifications never heard of" (see
    # nudgebee-enterprise#37885). With tenant_id set and
    # EVENT_ANALYSIS_ON_CHANNEL on, the incident-cache resolution must run
    # when reply_ref is absent, and its result -- deliberately distinct from
    # sent_notifications_result here -- must win.
    common_service = _FakeCommonService(
        sent_notifications_result=("STALE_CHANNEL", "STALE_THREAD", "T_STALE", "acc-stale"),
        incident_resolve_result=(
            "C_WORKFLOW",
            "1700000000.111111",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        ),
    )
    payload = LLMResponse(
        conversation_id=CONVERSATION_ID,
        type="final",
        response="Investigation done",
        tenant_id="tenant-1",
    )

    monkeypatch.setattr(llm_callbacks, "Session", lambda engine: _NullContext())
    monkeypatch.setattr(llm_callbacks, "is_feature_enabled", lambda session, flag, tenant_id: True)

    result = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    assert result == ("C_WORKFLOW", "1700000000.111111", "T05JWTTH3NH", "a2a30b02-0f67-42e5-a2ab-c658230fd798")
    assert common_service.resolve_incident_calls == ["756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"]


def test_handle_event_conversation_keeps_sent_notifications_account_id_when_incident_cache_lacks_it(monkeypatch):
    # resolve_incident_channel_and_thread's own required-fields check doesn't
    # cover account_id, so a cache entry missing it (several cache_event_entry
    # call sites in events.py never set the field) must not clobber a valid
    # account_id already found via sent_notifications.
    common_service = _FakeCommonService(
        sent_notifications_result=(
            "STALE_CHANNEL",
            "STALE_THREAD",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        ),
        incident_resolve_result=("C_WORKFLOW", "1700000000.333333", "T05JWTTH3NH", None),
    )
    payload = LLMResponse(
        conversation_id=CONVERSATION_ID,
        type="final",
        response="Investigation done",
        tenant_id="tenant-1",
    )

    monkeypatch.setattr(llm_callbacks, "Session", lambda engine: _NullContext())
    monkeypatch.setattr(llm_callbacks, "is_feature_enabled", lambda session, flag, tenant_id: True)

    result = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    assert result == (
        "C_WORKFLOW",
        "1700000000.333333",
        "T05JWTTH3NH",
        "a2a30b02-0f67-42e5-a2ab-c658230fd798",
    )


def test_handle_event_conversation_falls_back_to_sent_notifications_when_incident_cache_misses(monkeypatch):
    # A normal finding-alert-bound channel (sent_notifications has a row) was
    # never joined via slack.join_channel, so it has no incident-cache entry
    # for this fingerprint. resolve_incident_channel_and_thread correctly
    # returns all-None for it -- that must NOT overwrite the already-correct
    # sent_notifications-based destination with nothing.
    common_service = _FakeCommonService(
        sent_notifications_result=(
            "C_ALERT",
            "1700000000.000000",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        ),
        incident_resolve_result=(None, None, None, None),
    )
    payload = LLMResponse(
        conversation_id=CONVERSATION_ID,
        type="final",
        response="Investigation done",
        tenant_id="tenant-1",
    )

    monkeypatch.setattr(llm_callbacks, "Session", lambda engine: _NullContext())
    monkeypatch.setattr(llm_callbacks, "is_feature_enabled", lambda session, flag, tenant_id: True)

    result = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    assert result == ("C_ALERT", "1700000000.000000", "T05JWTTH3NH", "a2a30b02-0f67-42e5-a2ab-c658230fd798")


def test_handle_event_conversation_resolves_rca_report_with_correct_fingerprint(monkeypatch):
    # The on-demand RCA-report stage uses "event-rca-<fingerprint>" (a second
    # prefix layered on the plain "event-" every other stage uses) and never
    # sets reply_ref either, since it doesn't go through _process_event. A
    # naive split-on-first-hyphen used to pass "rca-<fingerprint>" as
    # incident_uuid, which matches no cached incident and drops the response
    # silently (nudgebee-enterprise#37885).
    rca_conversation_id = "event-rca-756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"
    common_service = _FakeCommonService(
        sent_notifications_result=(None, None, None, None),
        incident_resolve_result=(
            "C_WORKFLOW",
            "1700000000.222222",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        ),
    )
    payload = LLMResponse(
        conversation_id=rca_conversation_id,
        type="follow-up",
        response='{"question": "Which service owns this pod?"}',
        tenant_id="tenant-1",
    )

    monkeypatch.setattr(llm_callbacks, "Session", lambda engine: _NullContext())
    monkeypatch.setattr(llm_callbacks, "is_feature_enabled", lambda session, flag, tenant_id: True)

    result = _handle_event_conversation(common_service, rca_conversation_id, payload)

    assert result == ("C_WORKFLOW", "1700000000.222222", "T05JWTTH3NH", "a2a30b02-0f67-42e5-a2ab-c658230fd798")
    assert common_service.resolve_incident_calls == ["756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"]


def test_handle_event_conversation_falls_back_when_reply_ref_malformed(monkeypatch):
    # A reply_ref missing the channel_id half (e.g. corrupted or hand-crafted)
    # must not be trusted -- fall back rather than route to an empty channel_id.
    common_service = _FakeCommonService(sent_notifications_result=("C_ALERT", "1700000000.000000", "T05JWTTH3NH", None))
    payload = LLMResponse(
        conversation_id=CONVERSATION_ID,
        type="final",
        response="2+2 equals 4",
        tenant_id=None,
        reply_ref="-1786000000.000100",
    )

    result = _handle_event_conversation(common_service, CONVERSATION_ID, payload)

    assert result == ("C_ALERT", "1700000000.000000", "T05JWTTH3NH", None)
    assert common_service.get_channel_and_ts_calls == [(CONVERSATION_ID, None)]


def test_build_llm_payload_always_sets_reply_ref():
    from notifications_server.services.events import Events

    entry = {"text": "hi", "account_id": "a", "user_id": "u", "session_id": "s", "channel_id": "C1"}
    payload = Events.build_llm_payload(entry, "1786000000.000200")
    assert payload["reply_ref"] == "C1-1786000000.000200"


def test_build_llm_payload_snapshots_asker_ref_from_slack_user_id():
    """#42744: asker_ref must be this question's own snapshot of the cache entry's
    slack_user_id, taken when the request is built -- not re-read later from a
    thread's shared cache entry that a subsequent message could overwrite."""
    from notifications_server.services.events import Events

    entry = {
        "text": "hi",
        "account_id": "a",
        "user_id": "u",
        "session_id": "s",
        "channel_id": "C1",
        "slack_user_id": "U_ASKER",
    }
    payload = Events.build_llm_payload(entry, "1786000000.000200")
    assert payload["asker_ref"] == "U_ASKER"

    # The thread's shared cache entry is later overwritten by a different asker
    # (simulating a second user writing in the same thread); a dict already built
    # for the first question must not be affected.
    entry["slack_user_id"] = "U_OTHER"
    assert payload["asker_ref"] == "U_ASKER"


def test_build_llm_payload_omits_asker_ref_when_cache_entry_has_none():
    from notifications_server.services.events import Events

    entry = {"text": "hi", "account_id": "a", "user_id": "u", "session_id": "s", "channel_id": "C1"}
    payload = Events.build_llm_payload(entry, "1786000000.000200")
    assert "asker_ref" not in payload
