"""
Tests for the find-or-create "create channel/space" workflow path.

The "Create Channel" runbook task (runbook-server) posts to /api/channels/create.
For each platform CommonService.create_channel returns an existing destination
whose name matches, otherwise creates a new one ("created" flags which happened).

Covered here at the service layer (no live engine — the helpers use no DB state
beyond the install lookup, which is monkeypatched):
  * Slack    — name normalization + find-or-create + name_taken race fallback
  * MS Teams — team_id required + find-by-display-name
  * Google Chat — find-by-display-name + needs_authorization surfacing
"""

import pytest

from notifications_server.services import common
from notifications_server.services.common import CommonService


@pytest.fixture
def svc():
    # create_channel and its helpers use no instance state beyond the install
    # lookup (monkeypatched below), so bypass __init__ to avoid a live engine.
    return CommonService.__new__(CommonService)


class _Install:
    def __init__(self, token="xoxb-test", team_id="T1"):
        self.token = token
        self.team_id = team_id


# ------------------------------ name normalization ------------------------------


def test_slack_name_normalization():
    assert CommonService._normalize_slack_channel_name("Incident #42!") == "incident-42"
    assert CommonService._normalize_slack_channel_name("  Already-Good_Name  ") == "already-good_name"
    assert CommonService._normalize_slack_channel_name("a" * 100) == "a" * 80


# ----------------------------------- Slack -----------------------------------


def test_slack_returns_existing_channel(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(svc, "get_slack_channels", lambda mp: {"data": [{"name": "incident-42", "id": "C123"}]})

    result = svc.create_channel(platform="slack", tenant_id="t1", name="Incident #42")

    assert result["success"] is True
    assert result["created"] is False
    assert result["data"] == {"channel_id": "C123", "name": "incident-42", "platform": "slack"}


def test_slack_creates_when_missing(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(svc, "get_slack_channels", lambda mp: {"data": []})

    class _Client:
        def conversations_create(self, *, token, name, is_private, team_id):
            return {"ok": True, "channel": {"id": "C999", "name": name}}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.create_channel(platform="slack", tenant_id="t1", name="new-channel")

    assert result["created"] is True
    assert result["data"]["channel_id"] == "C999"
    assert result["data"]["name"] == "new-channel"


def test_slack_name_taken_falls_back_to_existing(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    # First lookup misses (so we attempt create); second lookup (after name_taken) hits.
    lookups = [{"data": []}, {"data": [{"name": "dup", "id": "C77"}]}]
    monkeypatch.setattr(svc, "get_slack_channels", lambda mp: lookups.pop(0))

    class _Client:
        def conversations_create(self, *, token, name, is_private, team_id):
            return {"ok": False, "error": "name_taken"}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.create_channel(platform="slack", tenant_id="t1", name="dup")

    assert result["success"] is True
    assert result["created"] is False
    assert result["data"]["channel_id"] == "C77"


def test_slack_no_installation_returns_error(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: None)
    result = svc.create_channel(platform="slack", tenant_id="t1", name="x")
    assert "error" in result


# --------------------------------- MS Teams ---------------------------------


def test_ms_teams_requires_team_id(svc):
    result = svc.create_channel(platform="ms_teams", tenant_id="t1", name="x")
    assert "error" in result
    assert "team_id" in result["error"]["message"]


def test_ms_teams_returns_existing_channel(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(svc, "_refresh_ms_teams_token", lambda mp: None)
    monkeypatch.setattr(
        common.MsTeamsClient, "list_team_channels", lambda token, team_id: [{"name": "General", "id": "19:abc"}]
    )

    result = svc.create_channel(platform="ms_teams", tenant_id="t1", name="General", team_id="team-1")

    assert result["created"] is False
    assert result["data"]["channel_id"] == "19:abc"
    assert result["data"]["team_id"] == "team-1"


def test_ms_teams_creates_when_missing(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(svc, "_refresh_ms_teams_token", lambda mp: None)
    monkeypatch.setattr(common.MsTeamsClient, "list_team_channels", lambda token, team_id: [])
    monkeypatch.setattr(
        common.MsTeamsClient,
        "create_channel",
        lambda **kw: {"success": True, "channel_id": "19:new", "name": kw["display_name"], "url": "http://x"},
    )

    result = svc.create_channel(platform="ms_teams", tenant_id="t1", name="Ops", team_id="team-1")

    assert result["created"] is True
    assert result["data"]["channel_id"] == "19:new"
    assert result["data"]["url"] == "http://x"


# -------------------------------- Google Chat --------------------------------


def test_google_chat_returns_existing_space(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(
        common.GoogleChatAppClient, "find_space_by_display_name", lambda name, tenant=None: "spaces/EXIST"
    )

    result = svc.create_channel(platform="google_chat", tenant_id="t1", name="Incident Room")

    assert result["created"] is False
    assert result["data"]["channel_id"] == "spaces/EXIST"


def test_google_chat_creates_when_missing(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(common.GoogleChatAppClient, "find_space_by_display_name", lambda name, tenant=None: None)
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "create_space",
        lambda name, tenant=None: {"success": True, "channel_id": "spaces/NEW", "name": name, "url": "http://s"},
    )

    result = svc.create_channel(platform="google_chat", tenant_id="t1", name="Incident Room")

    assert result["created"] is True
    assert result["data"]["channel_id"] == "spaces/NEW"


def test_google_chat_needs_authorization_returns_error(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(common.GoogleChatAppClient, "find_space_by_display_name", lambda name, tenant=None: None)
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "create_space",
        lambda name, tenant=None: {"success": False, "reason": "needs_authorization"},
    )

    result = svc.create_channel(platform="google_chat", tenant_id="t1", name="x")

    assert "error" in result
    assert "authorization" in result["error"]["message"].lower()


def test_google_chat_not_enabled_returns_error(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: False)
    result = svc.create_channel(platform="google_chat", tenant_id="t1", name="x")
    assert "error" in result
    assert "not configured" in result["error"]["message"].lower()


def test_unsupported_platform_returns_error(svc):
    result = svc.create_channel(platform="discord", tenant_id="t1", name="x")
    assert "error" in result
