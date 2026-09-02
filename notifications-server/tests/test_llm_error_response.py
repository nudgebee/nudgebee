"""Tests for the ``type: "error"`` LLM callback.

The LLM server reports a failed agent run by POSTing ``/llm/response`` with
``type: "error"`` and ``response`` set to the raw upstream error string. The
router had no handler for that type, so the callback 400'd and the chat thread
was left in silence — the user who asked got no reply at all.

What these pin:
  * every (platform, type) pair the router dispatches resolves to a handler,
    with an arity matching how that platform is called
  * each platform's error handler posts the generic copy
  * the raw upstream error is never posted to the thread
"""

import asyncio
import inspect

import pytest

from notifications_server.services import events as events_module
from notifications_server.services.bot_messages import GENERIC_ERROR_MESSAGES
from notifications_server.services.events import Events

RAW_ERROR = "llm: bedrock InvokeModel failed: ThrottlingException (arn:aws:bedrock:us-east-1:1234:model/x)"

# (handler name, params after self) per platform, mirroring the router's dispatch:
# Slack is called with (payload, cached_entry, channel_id, thread_ts, team_id);
# Teams and Google Chat with (payload, cached_entry, thread_ts).
DISPATCH_TABLE = {
    "slack": (5, ["handle_followup_response", "handle_final_response", "handle_error_response"]),
    "ms_teams": (3, ["handle_teams_followup_response", "handle_teams_final_response", "handle_teams_error_response"]),
    "google_chat": (
        3,
        ["handle_gchat_followup_response", "handle_gchat_final_response", "handle_gchat_error_response"],
    ),
}


class _Payload:
    def __init__(self, response=RAW_ERROR):
        self.conversation_id = "event-fceff81e90893fcc4f12cb6d493f287a"
        self.type = "error"
        self.response = response


class _FakeCommonService:
    def __init__(self):
        self.slack_messages = []
        self.teams_messages = []
        self.gchat_messages = []

    def slack_reply_in_thread(self, channel_id, team_id, thread_ts, message, transform_to_markdown=True):
        self.slack_messages.append(message)

    async def teams_reply_from_conversation_reference(self, conversation_ref, message, teams_id):
        self.teams_messages.append(message)

    def gchat_reply_in_thread(self, space_name, thread_name, message, tenant_id):
        self.gchat_messages.append(message)


@pytest.fixture
def svc(monkeypatch):
    # The error handlers use only common_service + the module-level cache, so
    # bypass __init__ to keep the unit test free of a live engine / Slack app.
    events = Events.__new__(Events)
    events.common_service = _FakeCommonService()
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda *a, **kw: None)
    return events


def test_router_dispatch_table_is_complete():
    for platform, (arity, handler_names) in DISPATCH_TABLE.items():
        for name in handler_names:
            handler = getattr(Events, name, None)
            assert handler is not None, f"{platform}: Events has no {name}"
            params = [p for p in inspect.signature(handler).parameters if p != "self"]
            assert len(params) == arity, f"{platform}: {name} takes {len(params)} args, router passes {arity}"


def test_slack_error_posts_generic_copy(svc):
    svc.handle_error_response(_Payload(), {}, "C123", "1779952241.483549", "T123")

    assert len(svc.common_service.slack_messages) == 1
    assert any(copy in svc.common_service.slack_messages[0] for copy in GENERIC_ERROR_MESSAGES)


def test_slack_error_mentions_the_asker(svc):
    svc.handle_error_response(_Payload(), {"slack_user_id": "U42"}, "C123", "1779952241.483549", "T123")

    assert svc.common_service.slack_messages[0].startswith("<@U42> ")


def test_teams_error_posts_generic_copy(svc):
    asyncio.run(svc.handle_teams_error_response(_Payload(), {"conversation_reference": {"id": "19:abc"}}, "19:abc"))

    assert len(svc.common_service.teams_messages) == 1
    assert svc.common_service.teams_messages[0] in GENERIC_ERROR_MESSAGES


def test_teams_error_without_conversation_reference_is_dropped(svc):
    asyncio.run(svc.handle_teams_error_response(_Payload(), {}, "19:abc"))

    assert svc.common_service.teams_messages == []


def test_gchat_error_posts_generic_copy(svc):
    svc.handle_gchat_error_response(_Payload(), {"space_name": "spaces/S", "tenant_id": "t1"}, "spaces/S/threads/T")

    assert len(svc.common_service.gchat_messages) == 1
    assert svc.common_service.gchat_messages[0] in GENERIC_ERROR_MESSAGES


def test_gchat_error_without_space_is_dropped(svc):
    svc.handle_gchat_error_response(_Payload(), {"tenant_id": "t1"}, "spaces/S/threads/T")

    assert svc.common_service.gchat_messages == []


@pytest.mark.parametrize(
    "call",
    [
        lambda s: s.handle_error_response(_Payload(), {"slack_user_id": "U42"}, "C1", "ts", "T1"),
        lambda s: asyncio.run(
            s.handle_teams_error_response(_Payload(), {"conversation_reference": {"id": "19:abc"}}, "19:abc")
        ),
        lambda s: s.handle_gchat_error_response(_Payload(), {"space_name": "spaces/S", "tenant_id": "t1"}, "t"),
    ],
    ids=["slack", "ms_teams", "google_chat"],
)
def test_raw_upstream_error_never_reaches_the_thread(svc, call):
    call(svc)

    posted = svc.common_service.slack_messages + svc.common_service.teams_messages + svc.common_service.gchat_messages
    assert posted, "handler posted nothing"
    for message in posted:
        assert RAW_ERROR not in message
        assert "bedrock" not in message.lower()
        assert "arn:aws" not in message.lower()
