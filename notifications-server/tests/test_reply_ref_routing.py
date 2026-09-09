"""Regression test for follow-up questions in a bound incident channel
replying as a new top-level message instead of into the thread they were
asked from.

Bug: a bound channel reuses the same "event-<fingerprint>" session_id/
conversation_id for every future @mention (see
CommonService._persist_channel_account_mapping). The llm-server response
webhook's _handle_event_conversation treated every response for such a
conversation_id as the original automated investigation's one-time
completion, unconditionally posting via get_incident_details_and_reply_on_channel
(which never threads) whenever EVENT_ANALYSIS_ON_CHANNEL is enabled -- so an
ordinary human follow-up question got the same non-threaded "announce"
treatment instead of replying into the thread it was actually asked from.

Fix: notifications-server attaches a per-question correlator, reply_ref
("{channel_id}-{thread_ts}"), to every chat request (events.py:build_llm_payload).
llm-server passes it through unchanged (ConversationApiRequest.ReplyRef ->
NBAgentRequest.ReplyRef -> the /llm/response webhook body) -- see
llm/llm-server's api/chains.go, agents/core/conversation.go,
agents/core/interface.go, agents/core/conversation_notification.go.
_handle_event_conversation now trusts reply_ref, when present, for
channel_id/thread_ts over the fingerprint-based sent_notifications guess, and
skips the announce path (which never sets reply_ref, since the original
investigation's completion never goes through _process_event). team_id/
account_id — stable properties of the bound channel, not per-question — still
come from the sent_notifications lookup either way.

These tests cover the notifications-server side:
  * build_llm_payload always includes a correct reply_ref, distinct from
    session_id (see also TestPayloadSeparation.test_reply_ref_identifies_the_physical_thread
    in test_channel_context.py).
  * _handle_event_conversation prefers a well-formed reply_ref over the
    fingerprint-based lookup and skips the announce path.
  * _handle_event_conversation falls back to the original behavior when
    reply_ref is absent or malformed.
"""

from notifications_server.routers import llm_callbacks
from notifications_server.routers.llm_callbacks import LLMResponse, _handle_event_conversation

CONVERSATION_ID = "event-756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"


class _FakeCommonService:
    """Records whether the fingerprint-lookup / announce paths were invoked, so
    tests can assert they're skipped once reply_ref is trusted."""

    def __init__(self, sent_notifications_result=(None, None, None, None)):
        self.sent_notifications_result = sent_notifications_result
        self.get_channel_and_ts_calls = []
        self.get_incident_details_calls = []

    def get_channel_and_ts_from_sent_notifications(self, conversation_id, tenant_id=None):
        self.get_channel_and_ts_calls.append((conversation_id, tenant_id))
        return self.sent_notifications_result

    def get_incident_details_and_reply_on_channel(self, incident_uuid, payload):
        self.get_incident_details_calls.append(incident_uuid)


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
    # non-threaded announce path must still be skipped -- reply_ref signals an
    # ongoing follow-up, not the original investigation's own completion.
    assert common_service.get_channel_and_ts_calls == [(CONVERSATION_ID, None)]
    assert common_service.get_incident_details_calls == []


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


def test_handle_event_conversation_still_announces_when_reply_ref_absent(monkeypatch):
    # The dangerous failure mode isn't "routes to the wrong thread" -- it's
    # "silently drops the original investigation's channel announcement".
    # With tenant_id set and EVENT_ANALYSIS_ON_CHANNEL on, the announce path
    # must still fire when reply_ref is absent.
    common_service = _FakeCommonService(
        sent_notifications_result=(
            "C_ALERT",
            "1700000000.000000",
            "T05JWTTH3NH",
            "a2a30b02-0f67-42e5-a2ab-c658230fd798",
        )
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
    assert common_service.get_incident_details_calls == [
        "756ce6c2bf3336dbad40190ef5ed02bf74b42b6484c22181aae76b8d96aac9ca"
    ]


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
