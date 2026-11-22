"""#42744: a Slack reply must be @mention-tagged to whoever asked the specific
question being answered, not to whoever most recently wrote in the thread.

Before the fix, handle_final_response/handle_followup_response/handle_error_response
built the mention from cached_entry['slack_user_id'] -- the thread's shared, live
cache entry, which a second message in the same thread overwrites in place before
the first question's async answer comes back. payload.asker_ref is this question's
own snapshot, echoed back unchanged by llm-server, and must win whenever present.
"""

import json

from notifications_server.services import events as events_module
from notifications_server.services.events import Events

CHANNEL = "C123"
TEAM = "T123"
THREAD = "1779952241.483549"


class _FakeCommonService:
    def __init__(self):
        self.sent = []

    def slack_reply_in_thread(self, channel_id, team_id, thread_ts, message, transform_to_markdown=True):
        self.sent.append(message)

    def slack_reply_in_thread_as_blocks(self, channel_id, team_id, thread_ts, blocks):
        self.sent.append(blocks)
        return "1779952242.000000"


class _Payload:
    def __init__(self, response, asker_ref=None, conversation_id=f"{CHANNEL}-{THREAD}", watch_id=None):
        self.response = response
        self.asker_ref = asker_ref
        self.conversation_id = conversation_id
        self.watch_id = watch_id


def _svc(monkeypatch):
    events = Events.__new__(Events)
    events.common_service = _FakeCommonService()
    monkeypatch.setattr(events_module.slack_progress, "stop_progress_stream", lambda *a, **k: None)
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda *a, **kw: None)
    return events


def _mention(sent_blocks) -> str:
    for block in sent_blocks:
        if block.get("type") == "context":
            for el in block.get("elements", []):
                text = el.get("text", "")
                if text.startswith("<@") and text.endswith(">"):
                    return text[2:-1]
    return ""


def test_handle_final_response_tags_asker_ref_over_stale_cached_entry(monkeypatch):
    events = _svc(monkeypatch)
    # The thread's shared cache entry has since been overwritten by a second
    # user writing in the same thread -- simulates the race from #42744.
    cached_entry = {"slack_user_id": "U_OTHER", "channel_id": CHANNEL}
    payload = _Payload("the answer", asker_ref="U_ASKER")

    events.handle_final_response(payload, cached_entry, CHANNEL, THREAD, TEAM)

    sent = events.common_service.sent
    assert len(sent) == 1
    assert _mention(sent[0]) == "U_ASKER"


def test_handle_final_response_falls_back_to_cached_entry_without_asker_ref(monkeypatch):
    """Requests sent before asker_ref existed carry no asker_ref -- must keep
    today's behavior rather than tag nobody."""
    events = _svc(monkeypatch)
    cached_entry = {"slack_user_id": "U_LEGACY", "channel_id": CHANNEL}
    payload = _Payload("the answer", asker_ref=None)

    events.handle_final_response(payload, cached_entry, CHANNEL, THREAD, TEAM)

    assert _mention(events.common_service.sent[0]) == "U_LEGACY"


def test_handle_followup_response_tags_asker_ref_over_stale_cached_entry(monkeypatch):
    events = _svc(monkeypatch)
    cached_entry = {"slack_user_id": "U_OTHER", "channel_id": CHANNEL}
    payload = _Payload(json.dumps({"question": "Which cluster?"}), asker_ref="U_ASKER")

    events.handle_followup_response(payload, cached_entry, CHANNEL, THREAD, TEAM)

    sent = events.common_service.sent
    assert len(sent) == 1
    assert _mention(sent[0]) == "U_ASKER"


def test_handle_error_response_tags_asker_ref_over_stale_cached_entry(monkeypatch):
    events = _svc(monkeypatch)
    cached_entry = {"slack_user_id": "U_OTHER", "channel_id": CHANNEL}
    payload = _Payload("some upstream error", asker_ref="U_ASKER")

    events.handle_error_response(payload, cached_entry, CHANNEL, THREAD, TEAM)

    sent = events.common_service.sent
    assert len(sent) == 1
    assert sent[0].startswith("<@U_ASKER>")
