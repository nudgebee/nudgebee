"""The event-analysis ("Ask Nubi to Analyse!") flow runs as an
`event-<fingerprint>` conversation. When a sub-agent calls ask_clarification it
becomes a WAITING `followup` message row on that conversation, and llm-server's
webhook for it can't be routed back to the Slack thread. These tests cover
`_surface_event_clarification` (poll the conversation, post the pending
followup through handle_followup_response) and the answer / retire / stale-click
paths that follow.
"""

import json
from types import SimpleNamespace
from unittest.mock import MagicMock

from notifications_server.services import actions_common
from notifications_server.services import events as events_module
from notifications_server.services.events import Events


def _ctx(**kw):
    base = dict(
        thread_ts="1786.1",
        channel_id="C1",
        team_id="T1",
        event_id="evt-1",
        account_id="acc",
        tenant_id="ten",
    )
    base.update(kw)
    return SimpleNamespace(**base)


def _followup_msg(mid="fm1", status="WAITING", agent="ag1"):
    return {
        "id": mid,
        "message_type": "followup",
        "status": status,
        "parent_agent_id": agent,
        "message": "Which namespace?",
        "message_config": json.dumps({"question": "Which namespace?", "followupOptions": ["a", "b"]}),
        "updated_at": "2026-09-02T10:00:00Z",
    }


# --- handle_followup_response marks the event-analysis flow ------------------


def test_handle_followup_response_marks_event_analysis_flow(monkeypatch):
    updates = []
    monkeypatch.setattr(events_module.event_cache, "update_event_entry", lambda ts, **kw: updates.append(kw))
    svc = Events.__new__(Events)
    svc.common_service = MagicMock()
    svc.common_service.slack_reply_in_thread_as_blocks.return_value = "1786.55"
    monkeypatch.setattr(events_module.slack_progress, "stop_progress_stream", lambda *a, **k: None)

    payload = SimpleNamespace(
        response=json.dumps({"question": "Which namespace?", "agent_id": "a1", "message_id": "m1"}),
        conversation_id="event-fp-1",
        session_id="event-fp-1",
    )
    svc.handle_followup_response(payload, {"slack_user_id": "U1", "account_id": "acc"}, "C1", "1786.1", "T1")

    merged = {k: v for u in updates for k, v in u.items()}
    assert merged["followup_session_id"] == "event-fp-1"
    assert merged["event_analysis_followup"] is True


# --- _surface_event_clarification ------------------------------------------


def test_surface_delivers_pending_clarification(monkeypatch):
    cache = MagicMock()
    cache.get_event_entry.return_value = {"slack_user_id": "U1"}
    conv = {"messages": [_followup_msg()], "agents": [{"id": "ag1", "message_id": "gen-9"}]}
    monkeypatch.setattr(actions_common, "_fetch_event_conversation", lambda *a: conv)
    service = MagicMock()
    service.event_service.cache = cache
    captured = {}
    service.event_service.handle_followup_response.side_effect = lambda p, *a: captured.update(
        {"resp": json.loads(p.response), "conv_id": p.conversation_id}
    )

    actions_common._surface_event_clarification(service, _ctx(), "user-1", "fp-1")

    assert captured["conv_id"] == "event-fp-1"
    assert captured["resp"]["question"] == "Which namespace?"
    assert captured["resp"]["followupOptions"] == ["a", "b"]
    assert captured["resp"]["agent_id"] == "ag1"
    assert captured["resp"]["message_id"] == "gen-9"
    cache.update_event_entry.assert_called_once()
    assert cache.update_event_entry.call_args.kwargs["event_followup_src_msg_id"] == "fm1"


def test_surface_noop_when_no_waiting_followup(monkeypatch):
    cache = MagicMock()
    cache.get_event_entry.return_value = {"slack_user_id": "U1"}
    conv = {"messages": [_followup_msg(status="COMPLETED"), {"message_type": "generation", "status": "IN_PROGRESS"}]}
    monkeypatch.setattr(actions_common, "_fetch_event_conversation", lambda *a: conv)
    service = MagicMock()
    service.event_service.cache = cache

    actions_common._surface_event_clarification(service, _ctx(), "user-1", "fp-1")

    service.event_service.handle_followup_response.assert_not_called()


def test_surface_noop_when_already_surfaced(monkeypatch):
    cache = MagicMock()
    cache.get_event_entry.return_value = {"event_followup_src_msg_id": "fm1"}
    monkeypatch.setattr(actions_common, "_fetch_event_conversation", lambda *a: {"messages": [_followup_msg("fm1")]})
    service = MagicMock()
    service.event_service.cache = cache

    actions_common._surface_event_clarification(service, _ctx(), "user-1", "fp-1")

    service.event_service.handle_followup_response.assert_not_called()


def test_surface_noop_when_thread_entry_gone(monkeypatch):
    cache = MagicMock()
    cache.get_event_entry.return_value = None
    fetched = []
    monkeypatch.setattr(actions_common, "_fetch_event_conversation", lambda *a: fetched.append(a))
    service = MagicMock()
    service.event_service.cache = cache

    actions_common._surface_event_clarification(service, _ctx(), "user-1", "fp-1")

    assert fetched == []
    service.event_service.handle_followup_response.assert_not_called()


def test_surface_retires_slack_message_when_answered_on_web(monkeypatch):
    # We surfaced fm1; it's now COMPLETED (answered on the web) and the Slack
    # keys are still set (a button answer would have cleared them).
    cache = MagicMock()
    cache.get_event_entry.return_value = {
        "event_followup_src_msg_id": "fm1",
        "event_analysis_followup": True,
        "followup_msg_ts": "1786.55",
        "followup_question": "Which namespace?",
    }
    monkeypatch.setattr(
        actions_common,
        "_fetch_event_conversation",
        lambda *a: {"messages": [_followup_msg("fm1", status="COMPLETED")]},
    )
    service = MagicMock()
    service.event_service.cache = cache
    service.event_service.build_blocks = Events.build_blocks

    actions_common._surface_event_clarification(service, _ctx(), "user-1", "fp-1")

    service.event_service.handle_followup_response.assert_not_called()
    blocks = service.common_service.update_slack_message_with_blocks.call_args.args[3]
    assert "web app" in blocks[0]["text"]["text"].lower()
    assert "event_analysis_followup" in cache.remove_event_keys.call_args.args[1]


# --- retire on poller exit ------------------------------------------------


def test_retire_strips_buttons_and_clears_keys_for_unanswered_followup():
    cache = MagicMock()
    cache.get_event_entry.return_value = {
        "event_analysis_followup": True,
        "followup_msg_ts": "1786.55",
        "followup_question": "Which namespace?",
    }
    service = MagicMock()
    service.event_service.cache = cache
    service.event_service.build_blocks = Events.build_blocks

    actions_common._retire_event_followup_message(service, _ctx())

    _, _, msg_ts, blocks = service.common_service.update_slack_message_with_blocks.call_args.args
    assert msg_ts == "1786.55"
    assert "finished" in blocks[0]["text"]["text"].lower()
    cleared = cache.remove_event_keys.call_args.args[1]
    # the "already surfaced" marker is deliberately kept, not cleared
    assert "event_analysis_followup" in cleared and "event_followup_src_msg_id" not in cleared


def test_retire_noop_when_no_pending_followup():
    cache = MagicMock()
    cache.get_event_entry.return_value = {"slack_user_id": "U1"}
    service = MagicMock()
    service.event_service.cache = cache

    actions_common._retire_event_followup_message(service, _ctx())

    service.common_service.update_slack_message_with_blocks.assert_not_called()
    cache.remove_event_keys.assert_not_called()


# --- stale-click guards --------------------------------------------------


def test_stale_followup_button_click_is_ignored(monkeypatch):
    svc = Events.__new__(Events)
    svc.cache = MagicMock()
    svc.cache.get_event_entry.return_value = {"account_id": "acc"}  # no followup_msg_ts
    monkeypatch.setattr(svc, "_submit_followup", MagicMock())
    monkeypatch.setattr(svc, "_reply_error", MagicMock())

    svc.update_followup_for_event({"action_id": "select_followup_option--nudgebee"}, "C1", "T1", "U1", "1786.1")

    svc._submit_followup.assert_not_called()
    svc._reply_error.assert_not_called()


def test_followup_click_from_superseded_message_is_ignored(monkeypatch):
    svc = Events.__new__(Events)
    svc.cache = MagicMock()
    svc.cache.get_event_entry.return_value = {"followup_msg_ts": "TS2", "agent_id": "A2", "message_id": "M2"}
    monkeypatch.setattr(svc, "_submit_followup", MagicMock())

    svc.update_followup_for_event({"action_id": "select_followup_option--nudgebee"}, "C1", "T1", "U1", "1786.1", "TS1")
    svc._submit_followup.assert_not_called()

    svc.update_followup_for_event({"action_id": "select_followup_option--nudgebee"}, "C1", "T1", "U1", "1786.1", "TS2")
    svc._submit_followup.assert_called_once()
