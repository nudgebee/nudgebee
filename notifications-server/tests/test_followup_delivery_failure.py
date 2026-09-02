"""Regression test: a failure while posting the follow-up question must not
leave the conversation silently stuck.

Bug: ``handle_followup_response`` only caught ``(json.JSONDecodeError,
KeyError)``. Any other failure while posting to Slack (e.g. the resolved
Slack installation being the wrong app when two bots share a team_id, or any
other Slack API error) propagated uncaught out of the handler, through
``/llm/response``, which turned it into an HTTP 500. llm-server only logs a
non-200 response from the notification server at Debug level, so the failure
was invisible: the follow-up question never reached the thread, the user had
no way to answer it, and the conversation sat WAITING indefinitely.

Fix: wrap the handler's body in the same broad ``except Exception`` pattern
already used by ``handle_error_response``/``handle_final_response``, so a
delivery failure degrades to a visible generic reply instead of a silent 500.
"""

import json

import pytest

from notifications_server.services import events as events_module
from notifications_server.services.bot_messages import GENERIC_ERROR_MESSAGES
from notifications_server.services.events import Events

THREAD_TS = "1779952241.483549"


class _Payload:
    def __init__(self, response):
        self.conversation_id = "C123-1779952241.483549"
        self.type = "follow-up"
        self.response = response


class _FailingCommonService:
    """Simulates get_slack_installation resolving the wrong (or no) Slack
    app and the post itself blowing up, same as a real SlackApiError would."""

    def __init__(self):
        self.slack_messages = []

    def slack_reply_in_thread_as_blocks(self, channel_id, team_id, thread_ts, blocks):
        raise RuntimeError("not_in_channel")

    def slack_reply_in_thread(self, channel_id, team_id, thread_ts, message, transform_to_markdown=True):
        self.slack_messages.append(message)


@pytest.fixture
def svc(monkeypatch):
    events = Events.__new__(Events)
    events.common_service = _FailingCommonService()
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda *a, **kw: None)
    return events


def test_followup_post_failure_falls_back_to_generic_reply(svc):
    payload = _Payload(json.dumps({"question": "Which account should I use?"}))

    svc.handle_followup_response(payload, {"slack_user_id": "U42"}, "C123", THREAD_TS, "T123")

    assert len(svc.common_service.slack_messages) == 1
    assert any(copy in svc.common_service.slack_messages[0] for copy in GENERIC_ERROR_MESSAGES)
    assert svc.common_service.slack_messages[0].startswith("<@U42> ")


def test_followup_post_failure_does_not_raise(svc):
    payload = _Payload(json.dumps({"question": "Which account should I use?"}))

    # Must not propagate: an uncaught exception here is exactly what turned
    # into the silent 500 this test guards against.
    svc.handle_followup_response(payload, {}, "C123", THREAD_TS, "T123")
