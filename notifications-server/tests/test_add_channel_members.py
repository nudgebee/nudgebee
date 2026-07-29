"""
Tests for the "add users to channel/space" workflow path.

The "Add Channel Members" runbook task (runbook-server) posts to
/api/channels/members/add. For each platform CommonService.add_channel_members
invites the given users and returns per-user "added", "already_members", and
"failed" lists. Adding an existing member is idempotent success, not an error.

Covered here at the service layer (no live engine — the install lookup and the
platform clients are monkeypatched):
  * Slack       — per-user invite + already_in_channel handling + no-install error,
                  plus channel-level errors (not_in_channel/is_archived) surfacing
                  as one actionable error instead of a per-user failure
  * MS Teams    — team_id required + client result passthrough
  * Google Chat — add-members + needs_authorization surfacing + not-configured error
"""

import pytest

from notifications_server.services import common
from notifications_server.services.common import CommonService


@pytest.fixture
def svc():
    # add_channel_members and its helpers use no instance state beyond the install
    # lookup (monkeypatched below), so bypass __init__ to avoid a live engine.
    return CommonService.__new__(CommonService)


class _Install:
    def __init__(self, token="xoxb-test", team_id="T1"):
        self.token = token
        self.team_id = team_id


# ----------------------------------- generic -----------------------------------


def test_empty_user_list_returns_error(svc):
    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=[])
    assert "error" in result


def test_unsupported_platform_returns_error(svc):
    result = svc.add_channel_members(platform="discord", tenant_id="t1", channel_id="C1", user_ids=["U1"])
    assert "error" in result


# ----------------------------------- Slack -----------------------------------


def test_slack_invites_users(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            return {"ok": True}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1", "U2"])

    assert result["success"] is True
    assert result["data"]["added"] == ["U1", "U2"]
    assert result["data"]["already_members"] == []
    assert result["data"]["failed"] == []


def test_slack_already_in_channel_is_not_a_failure(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            return {"ok": False, "error": "already_in_channel"}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1"])

    assert result["data"]["already_members"] == ["U1"]
    assert result["data"]["failed"] == []


def test_slack_reports_failed_user(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            return {"ok": False, "error": "user_not_found"}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1"])

    assert result["data"]["added"] == []
    assert result["data"]["failed"] == [{"user_id": "U1", "error": "user_not_found"}]


def test_slack_not_in_channel_is_a_channel_error_not_a_user_failure(monkeypatch, svc):
    """ "not_in_channel" means the *bot* is not in the channel, so it must surface
    as an actionable channel-level error rather than being pinned on each user."""
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    calls = []

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            calls.append(users)
            return {"ok": False, "error": "not_in_channel"}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1", "U2"])

    assert "error" in result
    assert "not in the channel" in result["error"]["message"].lower()
    # No per-user "failed" entry blaming the user for a bot-membership problem.
    assert "data" not in result
    # Bails out on the first user instead of burning a call per user.
    assert calls == ["U1"]


def test_slack_channel_level_error_from_api_exception(monkeypatch, svc):
    """The same classification applies when slack_sdk raises instead of returning."""
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    class _Resp(dict):
        pass

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            raise common.SlackApiError("archived", _Resp(error="is_archived"))

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1"])

    assert "error" in result
    assert "archived" in result["error"]["message"].lower()


def test_slack_per_user_failure_does_not_stop_the_batch(monkeypatch, svc):
    """A genuine per-user error stays per-user: the rest of the batch still runs."""
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())

    class _Client:
        def conversations_invite(self, *, token, channel_id, users):
            if users == "U1":
                return {"ok": False, "error": "user_is_restricted"}
            return {"ok": True}

    svc.slack_app = type("App", (), {"client": _Client()})()

    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1", "U2"])

    assert result["data"]["added"] == ["U2"]
    assert result["data"]["failed"] == [{"user_id": "U1", "error": "user_is_restricted"}]


def test_slack_no_installation_returns_error(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: None)
    result = svc.add_channel_members(platform="slack", tenant_id="t1", channel_id="C1", user_ids=["U1"])
    assert "error" in result


# --------------------------------- MS Teams ---------------------------------


def test_ms_teams_requires_team_id(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    result = svc.add_channel_members(platform="ms_teams", tenant_id="t1", channel_id="19:abc", user_ids=["U1"])
    assert "error" in result
    assert "team_id" in result["error"]["message"]


def test_ms_teams_client_rejects_invalid_user_id(monkeypatch):
    """A user_id with disallowed characters must never be interpolated into the
    Graph URL — it lands in "failed" without a network call, and valid ids in the
    same batch still go through."""
    from notifications_server.clients import ms_teams_client

    calls = []

    class _Resp:
        status_code = 201
        text = ""

    monkeypatch.setattr(ms_teams_client.requests, "post", lambda *a, **k: calls.append(a) or _Resp())

    result = ms_teams_client.MsTeamsClient.add_team_members(
        access_token="tok", team_id="team-1", user_ids=["../etc/passwd", "good-user"]
    )

    assert result["added"] == ["good-user"]
    assert [f["user_id"] for f in result["failed"]] == ["../etc/passwd"]
    # Only the valid user reached the network.
    assert len(calls) == 1


def test_ms_teams_passes_through_client_result(monkeypatch, svc):
    monkeypatch.setattr(svc, "_get_messaging_platform", lambda *a, **k: _Install())
    monkeypatch.setattr(svc, "_refresh_ms_teams_token", lambda mp: None)
    monkeypatch.setattr(
        common.MsTeamsClient,
        "add_team_members",
        lambda **kw: {"added": kw["user_ids"], "already_members": [], "failed": []},
    )

    result = svc.add_channel_members(
        platform="ms_teams", tenant_id="t1", channel_id="19:abc", user_ids=["U1", "U2"], team_id="team-1"
    )

    assert result["success"] is True
    assert result["data"]["added"] == ["U1", "U2"]
    assert result["data"]["team_id"] == "team-1"


# -------------------------------- Google Chat --------------------------------


def test_google_chat_adds_members(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "add_members",
        lambda space, user_ids, tenant=None: {
            "success": True,
            "channel_id": space,
            "added": user_ids,
            "already_members": [],
            "failed": [],
        },
    )

    result = svc.add_channel_members(
        platform="google_chat", tenant_id="t1", channel_id="spaces/AAA", user_ids=["users/1"]
    )

    assert result["success"] is True
    assert result["data"]["added"] == ["users/1"]
    assert result["data"]["channel_id"] == "spaces/AAA"


def test_google_chat_needs_authorization_returns_error(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: True)
    monkeypatch.setattr(
        common.GoogleChatAppClient,
        "add_members",
        lambda space, user_ids, tenant=None: {"success": False, "reason": "needs_authorization"},
    )

    result = svc.add_channel_members(
        platform="google_chat", tenant_id="t1", channel_id="spaces/AAA", user_ids=["users/1"]
    )

    assert "error" in result
    assert "authorization" in result["error"]["message"].lower()


def test_google_chat_not_enabled_returns_error(monkeypatch, svc):
    monkeypatch.setattr(common.GoogleChatAppClient, "is_enabled", lambda: False)
    result = svc.add_channel_members(
        platform="google_chat", tenant_id="t1", channel_id="spaces/AAA", user_ids=["users/1"]
    )
    assert "error" in result
    assert "not configured" in result["error"]["message"].lower()
