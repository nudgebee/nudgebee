from types import SimpleNamespace

import pytest

from notifications_server.services import actions_common as ac


@pytest.fixture
def svc():
    service = ac.SlackEventsService.__new__(ac.SlackEventsService)
    service.common_service = SimpleNamespace(app_id=None, post_slack_ephemeral_response=lambda *a, **k: None)
    service.event_service = SimpleNamespace(execute_event=lambda **kw: None)
    service.get_user_email = lambda slack_user_id, team_id: (None, "user@example.com")
    return service


def _dm_event(**overrides):
    event = {
        "type": "message",
        "channel": "D1",
        "channel_type": "im",
        "user": "U1",
        "text": "why is my pod crashing",
        "ts": "1750000000.000100",
    }
    event.update(overrides)
    return event


def _dm_data(**event_overrides):
    return {
        "team_id": "T1",
        "event_id": "Ev1",
        "event_context": "EC1",
        "event": _dm_event(**event_overrides),
        "api_app_id": "A1",
    }


def test_dm_message_routes_to_handle_direct_message(svc, monkeypatch):
    called = {}
    monkeypatch.setattr(svc, "_handle_direct_message", lambda *a, **k: called.setdefault("hit", True))
    monkeypatch.setattr(svc, "_handle_channel_message", lambda *a, **k: called.setdefault("wrong", True))
    svc.execute_event(_dm_data())
    assert called == {"hit": True}


def test_channel_message_does_not_route_to_dm_handler(svc, monkeypatch):
    called = {}
    monkeypatch.setattr(svc, "_handle_direct_message", lambda *a, **k: called.setdefault("wrong", True))
    monkeypatch.setattr(svc, "_handle_channel_message", lambda *a, **k: called.setdefault("hit", True))
    svc.execute_event(_dm_data(channel_type="channel"))
    assert called == {"hit": True}


def test_dm_drops_bot_echo(svc):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    svc._handle_direct_message(_dm_event(bot_id="B123"), "T1", "Ev1", "EC1", "D1")
    assert calls == []


@pytest.mark.parametrize("subtype", ["message_changed", "message_deleted", "message_replied"])
def test_dm_drops_ignored_subtypes(svc, subtype):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    svc._handle_direct_message(_dm_event(subtype=subtype), "T1", "Ev1", "EC1", "D1")
    assert calls == []


def test_dm_drops_event_without_user(svc):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    event = _dm_event()
    del event["user"]
    svc._handle_direct_message(event, "T1", "Ev1", "EC1", "D1")
    assert calls == []


def test_dm_starts_conversation_for_a_real_question(svc):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    svc._handle_direct_message(_dm_event(), "T1", "Ev1", "EC1", "D1")
    assert len(calls) == 1
    kw = calls[0]
    assert kw["team_id"] == "T1"
    assert kw["event_id"] == "Ev1"
    assert kw["event_context"] == "EC1"
    assert kw["user_email"] == "user@example.com"
    assert kw["channel_id"] == "D1"
    assert kw["thread_ts"] == "1750000000.000100"
    assert kw["slack_user_id"] == "U1"
    # A brand-new DM has no `thread_ts` and no `event_ts` field from Slack — both
    # must resolve to the same value (the message's own `ts`) so downstream
    # `is_thread_request = thread_ts != event_ts` correctly reads False instead
    # of misreading every fresh DM as a reply inside an existing thread.
    assert kw["thread_ts"] == kw["event_ts"]


def test_dm_thread_ts_reuses_existing_thread(svc):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    event = _dm_event(thread_ts="1750000000.000050")
    svc._handle_direct_message(event, "T1", "Ev1", "EC1", "D1")
    assert calls[0]["thread_ts"] == "1750000000.000050"
    # A real threaded reply's thread_ts (the root) differs from its own ts, and
    # event_ts must follow ts (not thread_ts) so is_thread_request still reads
    # True here — this is a genuine reply-in-thread, unlike the fresh-DM case.
    assert calls[0]["event_ts"] == "1750000000.000100"
    assert calls[0]["thread_ts"] != calls[0]["event_ts"]


def test_dm_posts_ephemeral_when_user_resolution_fails(svc):
    calls = []
    svc.event_service.execute_event = lambda **kw: calls.append(kw)
    ephemeral_calls = []
    svc.common_service.post_slack_ephemeral_response = lambda *a, **k: ephemeral_calls.append((a, k))
    svc.get_user_email = lambda slack_user_id, team_id: ("no email on file", None)
    svc._handle_direct_message(_dm_event(), "T1", "Ev1", "EC1", "D1")
    assert calls == []
    assert len(ephemeral_calls) == 1
