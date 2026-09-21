"""Regression test: _submit_followup (Slack button/dropdown/pending-text
follow-up answers) used to hardcode session_id to the plain
"{channel_id}-{thread_ts}" form via a separate _get_llm_request_payload
builder, instead of reusing cached_entry["session_id"] like every other chat
dispatch (events.py:build_llm_payload). In a bound incident channel, whose
session_id is "event-<fingerprint>" (see
CommonService._persist_channel_account_mapping), that mismatch had two real
consequences on llm-server's side (see llm-server's api/chains.go:352-394,
423-425):

  * Conversation fork: llm-server resolves ConversationId by looking up
    GetConversationBySession(account_id, session_id). The mismatched
    session_id never matches the bound conversation, so llm-server silently
    starts a brand-new, disconnected conversation for the follow-up answer --
    any later @mention in the same channel goes back to the real
    "event-<fingerprint>" conversation, missing this turn.
  * Budget misclassification: chains.go picks the budget module via
    strings.HasPrefix(request.SessionId, events.SessionIdPrefixEvent) -- the
    plain session_id billed this turn to ModuleUserInvestigation instead of
    ModuleInvestigation, inconsistent with the rest of the conversation.

Fix: _submit_followup now builds its payload via build_llm_payload (the same
builder every other chat dispatch uses) instead of the separate, buggy
_get_llm_request_payload, which is removed.
"""

import pytest

from notifications_server.services import events as events_module
from notifications_server.services.events import Events


class _FakeEventCache:
    def __init__(self):
        self.removed_keys = None

    def remove_event_keys(self, thread_ts, keys):
        self.removed_keys = (thread_ts, list(keys))
        return True


class _FakeCommonService:
    def __init__(self):
        self.updated_blocks = []
        self.context_replies = []

    def update_slack_message_with_blocks(self, channel_id, team_id, followup_msg_ts, blocks):
        self.updated_blocks.append((channel_id, team_id, followup_msg_ts, blocks))
        return True

    def slack_reply_in_thread_with_context(self, channel_id, team_id, thread_ts, message, context):
        self.context_replies.append((channel_id, team_id, thread_ts, message, context))


@pytest.fixture
def events_svc(monkeypatch):
    svc = Events.__new__(Events)
    svc.cache = _FakeEventCache()
    svc.common_service = _FakeCommonService()
    monkeypatch.setattr(svc, "_attach_images", lambda *a, **k: None)
    return svc


def _stub_dispatch(monkeypatch):
    captured = {}

    def _capture_query(payload, headers):
        captured["payload"] = payload
        captured["headers"] = headers

    monkeypatch.setattr(events_module.Events, "query_llm_server", staticmethod(_capture_query))
    monkeypatch.setattr(events_module.slack_progress, "start_progress_poller", lambda *a, **k: None)
    return captured


def test_submit_followup_uses_bound_conversation_session_id_and_reply_ref(monkeypatch, events_svc):
    thread_ts = "1786000000.000100"
    channel_id = "C0BRFTQCQ48"
    team_id = "T05JWTTH3NH"
    cached_entry = {
        "text": "original question",
        "account_id": "acc-1",
        "user_id": "user-1",
        "tenant_id": "tenant-1",
        "session_id": "event-fp-abc",
        "channel_id": channel_id,
        "agent_id": "agent-1",
        "message_id": "msg-1",
        "followup_msg_ts": "1786000000.000050",
        "followup_question": "Which cluster?",
    }
    captured = _stub_dispatch(monkeypatch)

    events_svc._submit_followup(cached_entry, channel_id, team_id, thread_ts, "U1", "prod-cluster")

    payload = captured["payload"]
    # session_id keeps the bound conversation link -- no more fork on llm-server.
    assert payload["session_id"] == "event-fp-abc"
    # reply_ref still identifies this exact turn's thread, since session_id is
    # now "event-"-prefixed and would otherwise hit the same ambiguous webhook
    # routing the rest of this fix addresses.
    assert payload["reply_ref"] == f"{channel_id}-{thread_ts}"
    assert payload["agent_id"] == "agent-1"
    assert payload["message_id"] == "msg-1"
    assert payload["query"] == "prod-cluster"
    assert events_svc.common_service.updated_blocks


def test_submit_followup_event_analysis_branch(monkeypatch, events_svc):
    """An "Ask Nubi to Analyse!" clarification answer resumes the analysis's own
    `event-<fingerprint>` conversation (followup_session_id), sends no reply_ref,
    and starts no chat progress panel -- the still-running event-analysis poller
    is the sole output path."""
    thread_ts = "1786000000.000300"
    channel_id = "C0BRFTQCQ48"
    team_id = "T05JWTTH3NH"
    cached_entry = {
        "text": "Analysis for event with id evt-1",
        "account_id": "acc-1",
        "user_id": "user-1",
        "tenant_id": "tenant-1",
        "session_id": f"{channel_id}-{thread_ts}",
        "channel_id": channel_id,
        "agent_id": "agent-9",
        "message_id": "msg-9",
        "followup_msg_ts": "1786000000.000250",
        "followup_question": "What is the correct namespace?",
        "followup_session_id": "event-fp-xyz",
        "event_analysis_followup": True,
    }
    captured = _stub_dispatch(monkeypatch)
    poller_calls = []
    monkeypatch.setattr(events_module.slack_progress, "start_progress_poller", lambda *a, **k: poller_calls.append(a))

    events_svc._submit_followup(cached_entry, channel_id, team_id, thread_ts, "U1", "kube-system")

    payload = captured["payload"]
    assert payload["session_id"] == "event-fp-xyz"
    assert "reply_ref" not in payload
    assert payload["agent_id"] == "agent-9"
    assert payload["message_id"] == "msg-9"
    assert poller_calls == []
    assert ("event_analysis_followup" in events_svc.cache.removed_keys[1]) and (
        "followup_session_id" in events_svc.cache.removed_keys[1]
    )


def test_submit_followup_plain_conversation_session_id_unaffected(monkeypatch, events_svc):
    thread_ts = "1786000000.000200"
    channel_id = "C0BRFTQCQ48"
    team_id = "T05JWTTH3NH"
    cached_entry = {
        "text": "original question",
        "account_id": "acc-1",
        "user_id": "user-1",
        "tenant_id": "tenant-1",
        "session_id": f"{channel_id}-{thread_ts}",
        "channel_id": channel_id,
    }
    captured = _stub_dispatch(monkeypatch)

    events_svc._submit_followup(cached_entry, channel_id, team_id, thread_ts, "U1", "yes")

    assert captured["payload"]["session_id"] == f"{channel_id}-{thread_ts}"
    assert captured["payload"]["reply_ref"] == f"{channel_id}-{thread_ts}"
    assert events_svc.common_service.context_replies
