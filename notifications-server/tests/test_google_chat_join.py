"""
Tests for the Google Chat "join space" workflow path (#30990).

The "Join Notification Channel" workflow action (api-server) posts to
/api/channels/join. For Google Chat the service-account bot self-joins the space,
posts the proactive message, and binds the resulting thread -> incident session so
follow-ups in that thread are answered with the incident's context (Slack parity).

Two layers are covered:
  * CommonService._join_google_chat_space — join + post + thread/session binding
  * Events._resolve_incident_binding     — the inbound tenant-match security gate
"""

import pytest

from notifications_server.services import common
from notifications_server.services.common import CommonService
from notifications_server.services.events import Events


@pytest.fixture
def svc():
    # _join_google_chat_space uses no instance state (no DB/engine), so bypass
    # __init__ to keep the unit test free of a live engine.
    return CommonService.__new__(CommonService)


def _patch_gchat(monkeypatch, *, enabled=True, join=None, post=None):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: enabled)
    monkeypatch.setattr(common.GoogleChatAppClient, "join_space", lambda space: join or {"success": True})
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "post_message",
        lambda space, message, tenant=None: post or {"success": True, "thread_name": "spaces/S/threads/T"},
    )


# ----------------------------- join + bind -----------------------------


def test_join_success_binds_incident_thread(monkeypatch, svc):
    _patch_gchat(monkeypatch)
    calls = []
    monkeypatch.setattr(common.cache, "cache_channel_session_mapping", lambda **kw: calls.append(kw) or True)

    result = svc.join_channel(
        platform="google_chat",
        account_id="a1",
        tenant_id="t1",
        channel_id="spaces/S",
        session_id="incident-123",
        text="RCA: Redis pool exhaustion likely.",
    )

    assert result["success"] is True
    assert result["data"]["thread_name"] == "spaces/S/threads/T"
    assert len(calls) == 1
    binding = calls[0]
    # thread is the binding key (channel_id); space is the namespace (team_id) so the
    # key pairs with the inbound get_channel_session_mapping(thread_name, space_name).
    assert binding["channel_id"] == "spaces/S/threads/T"
    assert binding["team_id"] == "spaces/S"
    assert binding["session_id"] == "incident-123"
    assert binding["account_id"] == "a1"
    assert binding["tenant_id"] == "t1"


def test_join_posts_provided_text(monkeypatch, svc):
    posted = {}
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(common.GoogleChatAppClient, "join_space", lambda space: {"success": True})
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "post_message",
        lambda space, message, tenant=None: posted.update(space=space, message=message)
        or {"success": True, "thread_name": "spaces/S/threads/T"},
    )
    monkeypatch.setattr(common.cache, "cache_channel_session_mapping", lambda **kw: True)

    svc.join_channel(
        platform="google_chat",
        account_id="a1",
        tenant_id="t1",
        channel_id="spaces/S",
        session_id="i1",
        text="hypothesis text",
    )
    assert posted == {"space": "spaces/S", "message": "hypothesis text"}


def test_join_needs_authorization_returns_error(monkeypatch, svc):
    _patch_gchat(monkeypatch, join={"success": False, "reason": "needs_authorization"})
    bind_called = []
    monkeypatch.setattr(common.cache, "cache_channel_session_mapping", lambda **kw: bind_called.append(kw))

    result = svc.join_channel(
        platform="google_chat",
        account_id="a1",
        tenant_id="t1",
        channel_id="spaces/S",
        session_id="i1",
        text="x",
    )
    assert "error" in result
    assert "authorization" in result["error"]["message"].lower()
    assert bind_called == []  # never bind a thread we did not join


def test_join_not_enabled_returns_error(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: False)
    result = svc.join_channel(
        platform="google_chat",
        account_id="a1",
        tenant_id="t1",
        channel_id="spaces/S",
        session_id="i1",
    )
    assert "error" in result
    assert "not configured" in result["error"]["message"].lower()


def test_join_without_session_skips_binding(monkeypatch, svc):
    _patch_gchat(monkeypatch)
    bind_called = []
    monkeypatch.setattr(common.cache, "cache_channel_session_mapping", lambda **kw: bind_called.append(kw))

    result = svc.join_channel(
        platform="google_chat",
        account_id="a1",
        tenant_id="t1",
        channel_id="spaces/S",
        session_id=None,
        text="x",
    )
    assert result["success"] is True
    assert bind_called == []


# ----------------------- inbound tenant-match gate -----------------------


def test_incident_binding_used_when_tenant_matches():
    binding = {"session_id": "incident-123", "account_id": "a1", "tenant_id": "t1"}
    session_id, account_id = Events._resolve_incident_binding(binding, "t1")
    assert session_id == "incident-123"
    assert account_id == "a1"


def test_incident_binding_ignored_on_tenant_mismatch():
    # A binding for a different tenant must never reuse its session/account.
    binding = {"session_id": "incident-123", "account_id": "a1", "tenant_id": "t1"}
    session_id, account_id = Events._resolve_incident_binding(binding, "t2")
    assert session_id is None
    assert account_id is None


def test_incident_binding_absent_returns_none():
    assert Events._resolve_incident_binding(None, "t1") == (None, None)
    assert Events._resolve_incident_binding({"tenant_id": "t1"}, "t1") == (None, None)


def test_incident_binding_ignored_when_tenant_id_missing_or_empty():
    # Guard against str(None) == str(None) → "None" == "None" falsely matching, and
    # against empty-string tenants. A null/empty tenant is never a valid match.
    assert Events._resolve_incident_binding({"session_id": "i1", "tenant_id": None}, None) == (None, None)
    assert Events._resolve_incident_binding({"session_id": "i1"}, None) == (None, None)
    assert Events._resolve_incident_binding({"session_id": "i1", "tenant_id": ""}, "") == (None, None)
    assert Events._resolve_incident_binding({"session_id": "i1", "tenant_id": "t1"}, None) == (None, None)
